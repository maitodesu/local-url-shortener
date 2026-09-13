local hash_key = KEYS[1]
local N = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local current_timestamp = tonumber(ARGV[3])
local idle_ttl_seconds = 5 * 60

local values = redis.call('HMGET', hash_key, 'tokens', 'last_refill')

local tokens = values[1]
local last_refill = tonumber(values[2])

if tokens == false then
    tokens = N
    last_refill = current_timestamp
end

tokens = tonumber(tokens)
local elapsed = current_timestamp - last_refill
local refilled = tokens + (elapsed * refill_rate)
tokens = math.min(N, refilled)

local allowed = false
if tokens >= 1 then
    allowed = true
    tokens = tokens - 1
end

-- set back the tokens and current_timestamp to hash KEY
redis.call('HSET', hash_key, 'tokens', tokens, 'last_refill', current_timestamp)

-- remove the hash_key for IP after 5 mins, like 5 min cooldown
redis.call('EXPIRE', hash_key, idle_ttl_seconds)

return allowed and 1 or 0
