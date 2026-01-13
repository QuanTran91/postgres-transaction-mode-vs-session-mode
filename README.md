# PGX Connection Pool Benchmark

Ever wondered how your Go app would handle thousands of concurrent requests hitting the database? This benchmark helps you find out.

We're testing pgx connection pools under realistic (and sometimes brutal) conditions - think 5,000 goroutines fighting over just 60 database connections. It's like Black Friday for your connection pool.

## What This Does

This benchmark simulates a real-world scenario: multiple Go servers (we're using 6) all trying to talk to the same PostgreSQL database through PgBouncer. Each server has its own connection pool, and we're intentionally creating a bottleneck to see what breaks first.

**The setup:**
- 6 separate connection pools (imagine 6 different server instances)
- Each pool can hold up to 10 connections
- 5,000 concurrent requests trying to use these 60 total connections
- That's an 83:1 ratio of requests to connections - ouch!

**What we're testing:**
- How fast can you actually get a connection when everyone wants one?
- Does PgBouncer's "session mode" vs "transaction mode" really matter?
- What happens after connections sit idle for a while?
- How much does pre-warming your connection pool help?

## The Results (Spoiler Alert)

Here's what we found:

**PgBouncer Transaction Mode:** 8,400 queries per second ⚡  
**PgBouncer Session Mode:** 42 queries per second 🐌

Yeah, that's a 200x difference. Session mode basically falls apart under high concurrency because it holds onto connections like they're going out of style.

## Quick Start

### Get it running

```bash
# Fire up PostgreSQL and PgBouncer
docker compose up -d

# Make sure everything's healthy
docker compose ps

# Run the benchmark
go run main.go
```

That's it. The benchmark will run for a minute or two, then spit out results to your console and save them to `benchmark_results.txt`.

## What's Actually Happening

When you run this, here's what goes down:

1. **Warmup run** - We let the pools establish connections and get comfortable
2. **Actual run** - Now we measure real performance with warmed-up pools
3. **Idle test** - We grab a connection, use it, let it sit for 10 seconds, then try to grab it again

For each test, we're tracking:
- How long it takes to get a connection (this is the killer metric)
- How long the actual query takes
- How many queries per second we can handle
- Which pool instance handled each request

## Understanding the Numbers

### Good signs:
- Query times under 500ms
- QPS above 5,000 (for transaction mode)
- Warmup giving you a 10%+ boost

### Warning signs:
- Query times over 1 second
- QPS under 1,000
- Lots of "failed to acquire connection" errors

### Red alerts:
- QPS under 100 (you're probably using session mode wrong)
- Query times over 60 seconds (things are timing out)
- "too many clients" errors (PostgreSQL is overwhelmed)

## The Architecture

Here's how it all fits together:

```
Your 5,000 concurrent requests
    ↓
Distributed across 6 connection pools (simulating 6 servers)
    ↓
Each pool has 10 connections max, 2 minimum
    ↓
All pools talk to PgBouncer (session or transaction mode)
    ↓
PgBouncer manages 50 connections to PostgreSQL
    ↓
PostgreSQL does the actual work
```

The bottleneck? We're intentionally limiting PgBouncer to 50 database connections while throwing 5,000 requests at it. This shows you what happens when your database can't keep up with demand.

## Configuration

Want to tweak things? Here's what you can change:

### In `main.go`:

```go
// Simulate more or fewer servers
NumberOfPoolInstances = 6

// Change how many requests you're testing with
concurrencyLevels := []int{5000}

// Adjust pool size per instance
DefaultMaxConnections = 10
DefaultMinConnections = 2  // Set this to 10 to pre-warm all connections
```

### In `pgbouncer/*.ini`:

```ini
max_client_conn = 10000      # How many clients can connect
max_db_connections = 50      # The bottleneck - connections to PostgreSQL
default_pool_size = 50       # PgBouncer's pool size
```

## What We Learned

### 1. Transaction mode is king for high concurrency

Session mode holds onto connections for the entire session. Transaction mode releases them immediately after each query. Under load, this makes a massive difference.

### 2. Pre-warming your pools matters

Setting `MinConnections = MaxConnections` gives you about 10% better throughput because all connections are ready to go from the start. No waiting for new connections to be established.

### 3. The query pattern matters

We're using `pool.Query()` and `rows.Close()` instead of manually calling `Acquire()` and `Release()`. This is how most real apps work, and it shows different behavior than the manual approach.

### 4. Multiple pools reveal the real bottleneck

When you simulate multiple servers (6 pools instead of 1), you see where the real bottleneck is - usually at the database or PgBouncer level, not in your application code.

## Common Issues

**"too many clients already"**  
PostgreSQL has a default limit of 100 connections. With 6 pools of 10 connections each, you're fine. But if you go direct to PostgreSQL without PgBouncer, you'll hit this limit fast.

**Session mode is crazy slow**  
That's expected! Session mode isn't designed for this kind of workload. It's meant for applications that hold connections for a while. For APIs that do quick queries, use transaction mode.

**Connections not getting released**  
Make sure you're calling `rows.Close()`. If you forget this, connections leak and your pool gets exhausted.

## Tweaking the Tests

### Want to test extreme scenarios?

```go
// 10,000 requests vs 60 connections - brutal
concurrencyLevels := []int{10000}

// More realistic load
concurrencyLevels := []int{1000}
```

### Want to see the impact of pre-warming?

```go
// All connections ready from the start
DefaultMinConnections = 10

// Connections created on demand
DefaultMinConnections = 2
```

Run both and compare the results. You'll see about 10% better performance with pre-warming.

## Project Structure

```
test-pgx/
├── docker-compose.yml           # Sets up PostgreSQL and PgBouncer
├── main.go                      # The benchmark code
├── pgbouncer/
│   ├── pgbouncer-session.ini    # Session mode config
│   ├── pgbouncer-transaction.ini # Transaction mode config
│   └── userlist.txt             # Auth credentials
├── init-db/
│   └── init.sql                 # Creates test table with 100 records
└── benchmark_results.txt        # Your results end up here
```

## Cleanup

When you're done:

```bash
# Stop everything
docker compose down

# Stop everything and delete the database
docker compose down -v
```

## The Bottom Line

If you're building a high-traffic API:
- Use PgBouncer in transaction mode
- Set your min connections equal to max connections
- Size your pools at 10-20 connections per server
- Monitor your connection acquisition times
- Don't use session mode unless you really need session-level features

And remember: this benchmark creates an intentional bottleneck. In production, you'd scale your database connections to match your load. But it's good to know what happens when you hit the limits.

## Learn More

- [pgx docs](https://github.com/jackc/pgx) - The Go PostgreSQL driver we're using
- [PgBouncer docs](https://www.pgbouncer.org/) - The connection pooler
- [PostgreSQL connection pooling](https://www.postgresql.org/docs/current/runtime-config-connection.html) - How PostgreSQL handles connections

---

Built this to understand connection pooling better? Same. Hope it helps! 🚀
