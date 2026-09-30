package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kanmon-hq/tollgate/internal/config"
	deliveryHttp "github.com/kanmon-hq/tollgate/internal/delivery/http"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/infrastructure/ratelimit"
	"github.com/kanmon-hq/tollgate/internal/infrastructure/sqlrepo"
	"github.com/kanmon-hq/tollgate/internal/usecase"
)

func main() {
	var (
		totalRequests int
		concurrency   int
		targetURL     string
		rawKey        string
	)

	flag.IntVar(&totalRequests, "n", 1000000, "Total number of requests")
	flag.IntVar(&concurrency, "c", 200, "Number of concurrent workers")
	flag.StringVar(&targetURL, "url", "", "Target URL (e.g. http://localhost:8080/billing/test). If empty, runs in-memory benchmark")
	flag.StringVar(&rawKey, "key", "", "API Key for external target URL")
	flag.Parse()

	fmt.Println("==========================================================")
	fmt.Println("  Tollgate High-Throughput Load Test (1,000,000 Requests)")
	fmt.Println("==========================================================")

	ctx := context.Background()
	requestURL := targetURL
	authKey := rawKey

	// 外部 URL 未指定時は、超高速インプロセス構成を起動
	if requestURL == "" {
		// 1. 下流のモックバックエンドサーバー
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}))
		defer backend.Close()

		// 2. Tollgate サーバーのセットアップ (SQLite :memory: + InMemory Limiter)
		sqliteRepo, err := sqlrepo.NewSQLiteRepository(ctx, ":memory:")
		if err != nil {
			log.Fatalf("failed to init sqlite repo: %v", err)
		}

		limiter := ratelimit.NewInMemoryRateLimiter(time.Minute)
		defer limiter.Stop()

		keyUsecase := usecase.NewKeyUsecase(sqliteRepo)
		verifyUsecase := usecase.NewVerifyUsecase(sqliteRepo, limiter)

		routes := []*config.RouteConfig{
			{
				Prefix:      "/bench",
				Target:      backend.URL,
				Scope:       "bench:access",
				StripPrefix: true,
			},
		}

		proxyHandler, err := deliveryHttp.NewMultiTargetProxy(routes, "", verifyUsecase)
		if err != nil {
			log.Fatalf("failed to init proxy: %v", err)
		}

		cfg := &config.Config{
			AdminAPIKey: "bench-master-admin-key",
		}
		router := deliveryHttp.NewRouter(cfg, keyUsecase, verifyUsecase, sqliteRepo, proxyHandler)

		ts := httptest.NewServer(router)
		defer ts.Close()

		// テスト用 API キー発行 (RPM 100,000,000)
		keyOut, err := keyUsecase.CreateKey(ctx, entity.CreateKeyInput{
			Name:         "1M Bench Key",
			TenantID:     "tenant-bench-1m",
			ServiceID:    "bench-service",
			Scopes:       []string{"bench:access"},
			RateLimitRPM: 100000000,
			MonthlyQuota: 0,
		})
		if err != nil {
			log.Fatalf("failed to create bench key: %v", err)
		}

		requestURL = ts.URL + "/bench/test"
		authKey = keyOut.RawKey
		fmt.Printf("[Mode] In-Process Gateway: %s\n", requestURL)
	} else {
		fmt.Printf("[Mode] External Gateway: %s\n", requestURL)
	}

	fmt.Printf("[Config] Total Requests: %d, Concurrency: %d workers\n", totalRequests, concurrency)
	fmt.Println("----------------------------------------------------------")

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: 1000,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
		},
		Timeout: 5 * time.Second,
	}

	var (
		wg           sync.WaitGroup
		successCount int64
		failCount    int64
		completed    int64
	)

	// サンプリング用（100万件全件保存のメモリ圧迫を防ぐため、10,000件をサンプリング）
	sampleRate := 100
	if totalRequests <= 10000 {
		sampleRate = 1
	}
	sampleCapacity := (totalRequests / sampleRate) + 1
	samples := make([]time.Duration, 0, sampleCapacity)
	var sampleMu sync.Mutex

	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	goroutinesBefore := runtime.NumGoroutine()

	start := time.Now()

	// プログレスバー・定期進捗出力用 Goroutine
	stopProgress := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		var lastCompleted int64
		lastTime := time.Now()

		for {
			select {
			case <-stopProgress:
				return
			case t := <-ticker.C:
				current := atomic.LoadInt64(&completed)
				delta := current - lastCompleted
				durationSec := t.Sub(lastTime).Seconds()
				instantRPS := float64(delta) / durationSec
				pct := float64(current) / float64(totalRequests) * 100
				elapsedSoFar := time.Since(start).Truncate(time.Millisecond)

				fmt.Printf("\r[Progress] %7d / %d (%5.1f%%) | Speed: %7.0f req/s | Elapsed: %v",
					current, totalRequests, pct, instantRPS, elapsedSoFar)

				lastCompleted = current
				lastTime = t
			}
		}
	}()

	// ワーカー処理
	reqChan := make(chan int, concurrency*100)
	go func() {
		for i := 0; i < totalRequests; i++ {
			reqChan <- i
		}
		close(reqChan)
	}()

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range reqChan {
				reqStart := time.Now()
				req, _ := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
				if authKey != "" {
					req.Header.Set("Authorization", "Bearer "+authKey)
				}

				resp, err := client.Do(req)
				dur := time.Since(reqStart)

				if err == nil && resp.StatusCode == http.StatusOK {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					atomic.AddInt64(&successCount, 1)
				} else {
					if resp != nil {
						_ = resp.Body.Close()
					}
					atomic.AddInt64(&failCount, 1)
				}

				if idx%sampleRate == 0 {
					sampleMu.Lock()
					samples = append(samples, dur)
					sampleMu.Unlock()
				}

				atomic.AddInt64(&completed, 1)
			}
		}()
	}

	wg.Wait()
	close(stopProgress)
	elapsed := time.Since(start)

	fmt.Println() // プログレス改行

	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	goroutinesAfter := runtime.NumGoroutine()

	// サンプリングレイテンシ集計
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	var p50, p90, p99, min, max time.Duration
	if len(samples) > 0 {
		min = samples[0]
		max = samples[len(samples)-1]
		p50 = samples[int(float64(len(samples))*0.50)]
		p90 = samples[int(float64(len(samples))*0.90)]
		p99 = samples[int(float64(len(samples))*0.99)]
	}

	rps := float64(totalRequests) / elapsed.Seconds()

	fmt.Println("\n==========================================================")
	fmt.Println("  1,000,000 Requests Load Test Summary")
	fmt.Println("==========================================================")
	fmt.Printf("Total Requests:       %d\n", totalRequests)
	fmt.Printf("Concurrency:          %d workers\n", concurrency)
	fmt.Printf("Total Time:           %v\n", elapsed.Truncate(time.Millisecond))
	fmt.Printf("Throughput (RPS):     %.2f req/sec\n", rps)
	fmt.Printf("Success Rate (200):   %d (%.2f%%)\n", successCount, float64(successCount)/float64(totalRequests)*100)
	fmt.Printf("Failure Count:        %d\n", failCount)
	fmt.Println("----------------------------------------------------------")
	fmt.Printf("Latency Min:          %v\n", min)
	fmt.Printf("Latency p50:          %v\n", p50)
	fmt.Printf("Latency p90:          %v\n", p90)
	fmt.Printf("Latency p99:          %v\n", p99)
	fmt.Printf("Latency Max:          %v\n", max)
	fmt.Println("----------------------------------------------------------")
	fmt.Printf("Goroutines (Before):  %d -> (After): %d\n", goroutinesBefore, goroutinesAfter)
	fmt.Printf("Memory Alloc (MB):    %.2f MB -> %.2f MB\n", float64(memBefore.Alloc)/(1024*1024), float64(memAfter.Alloc)/(1024*1024))
	fmt.Println("==========================================================")
}
