-- Token Bucket limiter (atomic)
-- KEYS[1] = bucket key
-- ARGV[1] = capacity (max tokens)
-- ARGV[2] = refill_per_ms (tokens per millisecond)
-- ARGV[3] = cost (tokens to consume)
-- ARGV[4] = ttl_ms (key expiration in ms)

local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_per_ms = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

-- Redis server time (seconds, microseconds)
local t = redis.call("TIME")
local now_ms = (t[1] * 1000) + math.floor(t[2] / 1000)

local data = redis.call("HMGET", key, "tokens", "ts")
local tokens = tonumber(data[1])
local ts = tonumber(data[2])

if tokens == nil or ts == nil then
  tokens = capacity
  ts = now_ms
end

local delta = now_ms - ts
if delta < 0 then
  delta = 0
end

if refill_per_ms > 0 then
  local refill = delta * refill_per_ms
  tokens = math.min(capacity, tokens + refill)
end

ts = now_ms

local allowed = 0
if tokens >= cost then
  allowed = 1
  tokens = tokens - cost
end

redis.call("HSET", key, "tokens", tokens, "ts", ts)
if ttl_ms ~= nil and ttl_ms > 0 then
  redis.call("PEXPIRE", key, ttl_ms)
end

local retry_after = 0
if allowed == 0 and refill_per_ms > 0 then
  local missing = cost - tokens
  if missing < 0 then
    missing = 0
  end
  retry_after = math.ceil(missing / refill_per_ms)
end

local reset_after = 0
if refill_per_ms > 0 then
  local need = capacity - tokens
  if need < 0 then
    need = 0
  end
  reset_after = math.ceil(need / refill_per_ms)
end

return {allowed, tokens, retry_after, reset_after}
