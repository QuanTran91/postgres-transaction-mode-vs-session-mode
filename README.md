# PGX Connection Pool Benchmark

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

