package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectionType represents different connection modes
type ConnectionType string

const (
	DirectPostgres       ConnectionType = "direct-postgres"
	PgBouncerSession     ConnectionType = "pgbouncer-session"
	PgBouncerTransaction ConnectionType = "pgbouncer-transaction"
)

// Connection pool configuration constants
const (
	DefaultMaxConnections        = 10 // Bottleneck: 3000 goroutines competing for 10 connections
	DefaultMinConnections        = 2
	DefaultMaxConnLifetime       = 30 * time.Minute
	DefaultMaxConnIdleTime       = 30 * time.Second // Reduced from 10 minutes for faster connection release
	DefaultHealthCheckPeriod     = 30 * time.Second
	DefaultMaxConnLifetimeJitter = 3 * time.Minute
	NumberOfPoolInstances        = 6 // Simulate multiple Go server instances (each with own pool)
)

// BenchmarkResult stores metrics for a single benchmark run
type BenchmarkResult struct {
	ConnectionType     ConnectionType
	Concurrency        int
	IsWarmup           bool
	TotalDuration      time.Duration
	AvgAcquisitionTime time.Duration
	MinAcquisitionTime time.Duration
	MaxAcquisitionTime time.Duration
	QueriesPerSecond   float64
	TotalQueries       int
	AcquisitionTimes   []time.Duration
}

// Config holds connection configuration
type Config struct {
	ConnType ConnectionType
	DSN      string
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	fmt.Println("==========================================================")
	fmt.Println("PGX Connection Pool Benchmark")
	fmt.Println("Testing: Direct PostgreSQL, PgBouncer Session & Transaction Modes")
	fmt.Printf("Pool Config: MaxConns=%d, MinConns=%d, MaxIdleTime=%v\n",
		DefaultMaxConnections, DefaultMinConnections, DefaultMaxConnIdleTime)
	fmt.Println("==========================================================\n")

	// Connection configurations
	configs := []Config{
		{
			ConnType: PgBouncerSession,
			DSN:      "postgres://benchuser:benchpass@localhost:6432/benchdb?sslmode=disable",
		},
		{
			ConnType: PgBouncerTransaction,
			DSN:      "postgres://benchuser:benchpass@localhost:6433/benchdb?sslmode=disable",
		},
	}

	// Concurrency levels to test
	concurrencyLevels := []int{5000}

	// Store all results
	var allResults []BenchmarkResult

	// Run benchmarks for each configuration
	for _, config := range configs {
		fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("Testing: %s\n", config.ConnType)
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

		for _, concurrency := range concurrencyLevels {
			// Warmup run
			fmt.Printf("Warmup Run - Concurrency: %d\n", concurrency)
			warmupResult := runBenchmark(config, concurrency, true)
			allResults = append(allResults, warmupResult)

			// Wait a bit between warmup and actual run
			time.Sleep(2 * time.Second)

			// Actual benchmark run
			fmt.Printf("⚡ Actual Run - Concurrency: %d\n", concurrency)
			actualResult := runBenchmark(config, concurrency, false)
			allResults = append(allResults, actualResult)

			// Show comparison
			showComparison(warmupResult, actualResult)

			// Wait between different concurrency levels
			time.Sleep(1 * time.Second)
		}

		// Test idle/release/reacquire scenario
		fmt.Printf("\n⏸Testing Idle Connection Release (10s idle period)\n")
		idleResult := runIdleTest(config)
		fmt.Printf("Idle Test Result: Avg reacquisition time: %v\n\n", idleResult)
	}

	// Generate final report
	generateReport(allResults)
}

// runBenchmark executes a benchmark with specified concurrency
func runBenchmark(config Config, concurrency int, isWarmup bool) BenchmarkResult {
	ctx := context.Background()

	// Create multiple pool instances to simulate multiple Go server instances
	pools := make([]*pgxpool.Pool, NumberOfPoolInstances)
	for i := 0; i < NumberOfPoolInstances; i++ {
		poolConfig, err := pgxpool.ParseConfig(config.DSN)
		if err != nil {
			log.Fatalf("Unable to parse config for pool %d: %v\n", i, err)
		}

		// Apply pool configuration constants
		poolConfig.MaxConns = int32(DefaultMaxConnections)
		poolConfig.MinConns = int32(DefaultMinConnections)
		poolConfig.MaxConnLifetime = DefaultMaxConnLifetime
		poolConfig.MaxConnIdleTime = DefaultMaxConnIdleTime
		poolConfig.HealthCheckPeriod = DefaultHealthCheckPeriod
		poolConfig.MaxConnLifetimeJitter = DefaultMaxConnLifetimeJitter

		pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			log.Fatalf("Unable to create connection pool %d: %v\n", i, err)
		}
		pools[i] = pool
		defer pool.Close()
	}

	// Wait for pools to be ready
	time.Sleep(500 * time.Millisecond)

	var wg sync.WaitGroup
	acquisitionTimes := make([]time.Duration, concurrency)
	startTime := time.Now()

	// Launch concurrent workers, distributing them across pool instances
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Assign worker to a pool instance (round-robin distribution)
			poolIndex := workerID % NumberOfPoolInstances
			pool := pools[poolIndex]

			// Execute query - pool automatically acquires connection
			queryStart := time.Now()
			log.Printf("[QUERY START] Worker %d | Pool Instance %d | Type: %s | Goroutine: %d | Time: %s",
				workerID, poolIndex, config.ConnType, getGoroutineID(), queryStart.Format(time.RFC3339Nano))

			rows, err := pool.Query(ctx, "SELECT id, name FROM benchmark_data WHERE id = $1", (workerID%100)+1)
			if err != nil {
				log.Printf("[ERROR] Worker %d (Pool %d) query failed: %v", workerID, poolIndex, err)
				acquisitionTimes[workerID] = 0
				return
			}

			queryDuration := time.Since(queryStart)
			acquisitionTimes[workerID] = queryDuration

			log.Printf("[QUERY END] Worker %d | Pool Instance %d | Type: %s | Duration: %v",
				workerID, poolIndex, config.ConnType, queryDuration)

			// Read results
			var count int
			var name string
			if rows.Next() {
				err = rows.Scan(&count, &name)
				if err != nil {
					log.Printf("[ERROR] Worker %d (Pool %d) scan failed: %v", workerID, poolIndex, err)
				} else {
					log.Printf("[RESULT] Worker %d | Pool Instance %d | Result: id=%d, name=%s",
						workerID, poolIndex, count, name)
				}
			}

			// Close rows - this releases the connection back to pool
			closeStart := time.Now()
			rows.Close()
			closeDuration := time.Since(closeStart)

			log.Printf("[CLOSE] Worker %d | Pool Instance %d | Duration: %v", workerID, poolIndex, closeDuration)
		}(i)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	// Calculate metrics (now measuring query time instead of pure acquisition)
	var totalQueryTime time.Duration
	minQueryTime := acquisitionTimes[0]
	maxQueryTime := acquisitionTimes[0]

	for _, t := range acquisitionTimes {
		if t == 0 {
			continue // Skip failed queries
		}
		totalQueryTime += t
		if t < minQueryTime || minQueryTime == 0 {
			minQueryTime = t
		}
		if t > maxQueryTime {
			maxQueryTime = t
		}
	}

	avgQueryTime := totalQueryTime / time.Duration(concurrency)
	qps := float64(concurrency) / totalDuration.Seconds()

	result := BenchmarkResult{
		ConnectionType:     config.ConnType,
		Concurrency:        concurrency,
		IsWarmup:           isWarmup,
		TotalDuration:      totalDuration,
		AvgAcquisitionTime: avgQueryTime, // Now represents query time
		MinAcquisitionTime: minQueryTime,
		MaxAcquisitionTime: maxQueryTime,
		QueriesPerSecond:   qps,
		TotalQueries:       concurrency,
		AcquisitionTimes:   acquisitionTimes,
	}

	printResult(result)
	return result
}

// runIdleTest tests connection reacquisition after idle period
func runIdleTest(config Config) time.Duration {
	ctx := context.Background()

	poolConfig, err := pgxpool.ParseConfig(config.DSN)
	if err != nil {
		log.Fatalf("Unable to parse config: %v\n", err)
	}

	// Apply pool configuration constants
	poolConfig.MaxConns = int32(DefaultMaxConnections)
	poolConfig.MinConns = int32(DefaultMinConnections)
	poolConfig.MaxConnLifetime = DefaultMaxConnLifetime
	poolConfig.MaxConnIdleTime = DefaultMaxConnIdleTime
	poolConfig.HealthCheckPeriod = DefaultHealthCheckPeriod
	poolConfig.MaxConnLifetimeJitter = DefaultMaxConnLifetimeJitter

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatalf("Unable to create connection pool: %v\n", err)
	}
	defer pool.Close()

	// First acquisition
	log.Printf("[IDLE TEST] First acquisition - Type: %s", config.ConnType)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		log.Fatalf("Failed to acquire connection: %v", err)
	}

	// Execute query
	var count int
	err = conn.QueryRow(ctx, "SELECT COUNT(*) FROM benchmark_data").Scan(&count)
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}
	log.Printf("[IDLE TEST] First query executed, count: %d", count)

	// Release connection
	conn.Release()
	log.Printf("[IDLE TEST] Connection released, waiting 10 seconds...")

	// Wait 10 seconds (idle period)
	time.Sleep(10 * time.Second)

	// Reacquire connection
	log.Printf("[IDLE TEST] Reacquiring connection after 10s idle")
	reacquireStart := time.Now()
	conn, err = pool.Acquire(ctx)
	if err != nil {
		log.Fatalf("Failed to reacquire connection: %v", err)
	}
	reacquireDuration := time.Since(reacquireStart)

	log.Printf("[IDLE TEST] Reacquisition completed in %v", reacquireDuration)

	// Execute query again
	err = conn.QueryRow(ctx, "SELECT COUNT(*) FROM benchmark_data").Scan(&count)
	if err != nil {
		log.Fatalf("Query failed: %v", err)
	}
	log.Printf("[IDLE TEST] Second query executed, count: %d", count)

	conn.Release()

	return reacquireDuration
}

// printResult prints benchmark result
func printResult(result BenchmarkResult) {
	runType := "Actual"
	if result.IsWarmup {
		runType = "Warmup"
	}

	fmt.Printf("\n%s Results:\n", runType)
	fmt.Printf("   Total Duration:        %v\n", result.TotalDuration)
	fmt.Printf("   Avg Acquisition Time:  %v\n", result.AvgAcquisitionTime)
	fmt.Printf("   Min Acquisition Time:  %v\n", result.MinAcquisitionTime)
	fmt.Printf("   Max Acquisition Time:  %v\n", result.MaxAcquisitionTime)
	fmt.Printf("   Queries Per Second:    %.2f\n", result.QueriesPerSecond)
	fmt.Printf("   Total Queries:         %d\n\n", result.TotalQueries)
}

// showComparison shows warmup vs actual comparison
func showComparison(warmup, actual BenchmarkResult) {
	fmt.Printf("📈 Warmup vs Actual Comparison:\n")

	durationImprovement := float64(warmup.TotalDuration-actual.TotalDuration) / float64(warmup.TotalDuration) * 100
	avgAcqImprovement := float64(warmup.AvgAcquisitionTime-actual.AvgAcquisitionTime) / float64(warmup.AvgAcquisitionTime) * 100

	fmt.Printf("   Total Duration:       %v → %v (%.2f%% improvement)\n",
		warmup.TotalDuration, actual.TotalDuration, durationImprovement)
	fmt.Printf("   Avg Acquisition Time: %v → %v (%.2f%% improvement)\n",
		warmup.AvgAcquisitionTime, actual.AvgAcquisitionTime, avgAcqImprovement)
	fmt.Printf("   QPS:                  %.2f → %.2f\n\n",
		warmup.QueriesPerSecond, actual.QueriesPerSecond)
}

// generateReport generates final summary report
func generateReport(results []BenchmarkResult) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("FINAL BENCHMARK REPORT")
	fmt.Println(strings.Repeat("=", 80))

	// Group by connection type
	byType := make(map[ConnectionType][]BenchmarkResult)
	for _, r := range results {
		byType[r.ConnectionType] = append(byType[r.ConnectionType], r)
	}

	// Create report file
	f, err := os.Create("benchmark_results.txt")
	if err != nil {
		log.Printf("Failed to create report file: %v", err)
		return
	}
	defer f.Close()

	reportContent := "PGX Connection Pool Benchmark Results\n"
	reportContent += fmt.Sprintf("Generated: %s\n\n", time.Now().Format(time.RFC3339))

	for connType, typeResults := range byType {
		reportContent += fmt.Sprintf("\n%s\n", strings.Repeat("-", 80))
		reportContent += fmt.Sprintf("Connection Type: %s\n", connType)
		reportContent += fmt.Sprintf("%s\n\n", strings.Repeat("-", 80))

		for _, r := range typeResults {
			runType := "Actual"
			if r.IsWarmup {
				runType = "Warmup"
			}

			reportContent += fmt.Sprintf("Concurrency: %d (%s)\n", r.Concurrency, runType)
			reportContent += fmt.Sprintf("  Total Duration:       %v\n", r.TotalDuration)
			reportContent += fmt.Sprintf("  Avg Acquisition:      %v\n", r.AvgAcquisitionTime)
			reportContent += fmt.Sprintf("  Min Acquisition:      %v\n", r.MinAcquisitionTime)
			reportContent += fmt.Sprintf("  Max Acquisition:      %v\n", r.MaxAcquisitionTime)
			reportContent += fmt.Sprintf("  QPS:                  %.2f\n\n", r.QueriesPerSecond)
		}
	}

	f.WriteString(reportContent)
	fmt.Println(reportContent)
	fmt.Printf("\nFull report saved to: benchmark_results.txt\n")
}

// getGoroutineID returns the current goroutine ID
func getGoroutineID() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	var id uint64
	fmt.Sscanf(string(b), "goroutine %d ", &id)
	return id
}
