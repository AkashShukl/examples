package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

const (
	ttlMillis = 300
	lockKey           = "fencing-demo:lock"
	sequenceKey       = "fencing-demo:sequence"
	unsafeResourceKey = "fencing-demo:unsafe-resource"
	fencedResourceKey = "fencing-demo:fenced-resource"
)

// Atomic on a single primary, but NOT failover-proof.
const acquireFencedLua = `
if redis.call('EXISTS', KEYS[1]) == 1 then return -1 end
local token = redis.call('INCR', KEYS[2])
redis.call('PSETEX', KEYS[1], ARGV[1], ARGV[2])
return token
`

// Safe release avoids deleting a newer owner's lock.
const releaseLua = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`

// The resource checks the token atomically with the protected write.
const fencedWriteLua = `
local previous = tonumber(redis.call('HGET', KEYS[1], 'last_token') or '0')
local token = tonumber(ARGV[1])
if token < previous then return 0 end
redis.call('HSET', KEYS[1], 'last_token', ARGV[1], 'value', ARGV[2])
return 1
`

type experiment struct {
	client redisClient
	ctx    context.Context
}

func (e experiment) must(args ...string) any {
	result, err := e.client.Do(e.ctx, args...)
	if err != nil { panic(err) }
	return result
}

func (e experiment) reset(keys ...string) {
	e.must(append([]string{"DEL"}, keys...)...)
}

func (e experiment) acquireUnsafe(owner string) bool {
	reply := e.must("SET", lockKey, owner, "NX", "PX", fmt.Sprint(ttlMillis))
	return reply == "OK"
}

func (e experiment) acquireFenced(owner string) int64 {
	result := e.must("EVAL", acquireFencedLua, "2", lockKey, sequenceKey, fmt.Sprint(ttlMillis), owner)
	return result.(int64)
}

func (e experiment) release(owner string) bool {
	result := e.must("EVAL", releaseLua, "1", lockKey, owner)
	return result.(int64) == 1
}

func (e experiment) fencedWrite(token int64, value string) bool {
	result := e.must("EVAL", fencedWriteLua, "1", fencedResourceKey, fmt.Sprint(token), value)
	return result.(int64) == 1
}

func runUnsafe(e experiment) {
	fmt.Println("\n=== WITHOUT FENCING ===")
	e.reset(lockKey, unsafeResourceKey)
	if !e.acquireUnsafe("owner-A") { panic("A could not get initial lease") }
	fmt.Println("A acquired 300ms Redis lease; simulated work pauses")
	time.Sleep(450 * time.Millisecond)
	if !e.acquireUnsafe("owner-B") { panic("B could not get expired lease") }
	fmt.Println("Lease expired; B acquired the same lock")
	e.must("SET", unsafeResourceKey, "newer-write-from-B")
	fmt.Println("B wrote: newer-write-from-B")
	e.must("SET", unsafeResourceKey, "stale-write-from-A")
	fmt.Println("A resumed and overwrote B, despite expired lease")
	fmt.Printf("Final value: %v  <-- WRONG\n", e.must("GET", unsafeResourceKey))
	fmt.Printf("A's attempted unlock removed B's lock: %t (must be false)\n", e.release("owner-A"))
	e.release("owner-B")
}

func runFenced(e experiment) {
	fmt.Println("\n=== WITH RESOURCE-ENFORCED FENCING ===")
	e.reset(lockKey, sequenceKey, fencedResourceKey)
	tokenA := e.acquireFenced("owner-A")
	if tokenA < 1 { panic("A could not get initial lease") }
	fmt.Printf("A acquired lease, token=%d; simulated work pauses\n", tokenA)
	time.Sleep(450 * time.Millisecond)
	tokenB := e.acquireFenced("owner-B")
	if tokenB <= tokenA { panic("B did not acquire a strictly newer fencing token") }
	fmt.Printf("Lease expired; B acquired lease, token=%d\n", tokenB)
	if !e.fencedWrite(tokenB, "newer-write-from-B") { panic("B write unexpectedly rejected") }
	fmt.Println("Resource accepted B's newer write")
	acceptedA := e.fencedWrite(tokenA, "stale-write-from-A")
	fmt.Printf("Resource accepted A's stale write: %t (must be false)\n", acceptedA)
	fmt.Printf("Final value: %v  <-- CORRECT\n", e.must("HGET", fencedResourceKey, "value"))
	fmt.Printf("Last accepted token: %v\n", e.must("HGET", fencedResourceKey, "last_token"))
	if acceptedA { panic("fencing invariant violated: stale A was accepted") }
	e.release("owner-A")
	e.release("owner-B")
}

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" { addr = "127.0.0.1:6379" }
	e := experiment{client: redisClient{addr: addr}, ctx: context.Background()}
	if _, err := e.client.Do(e.ctx, "PING"); err != nil {
		fmt.Fprintf(os.Stderr, "Redis unavailable: %v\nStart it with: docker compose up -d\n", err)
		os.Exit(1)
	}
	runUnsafe(e)
	runFenced(e)
}
