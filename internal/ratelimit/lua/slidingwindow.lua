-- Sliding window rate limiter using sorted sets.
-- Atomic: the entire check-and-increment runs as one Redis operation.
local key       = KEYS[1]
local now       = tonumber(ARGV[1])   -- current time in ms
local window_ms = tonumber(ARGV[2])   -- window size in ms
local limit     = tonumber(ARGV[3])   -- max requests allowed
local req_id    = ARGV[4]             -- unique request ID

-- Remove entries outside the current window
redis.call('ZREMRANGEBYSCORE', key, 0, now - window_ms)

-- Count remaining entries
local count = redis.call('ZCARD', key)

if count < limit then
    redis.call('ZADD', key, now, req_id)
    redis.call('PEXPIRE', key, window_ms)
    return {0, limit - count - 1}  -- {allowed, remaining}
end

-- Blocked: find when the oldest entry expires
local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
return {1, tonumber(oldest[2]) + window_ms}  -- {blocked, reset_at_ms}
