-- KEYS[1]: limit_key (如 rate:rpm:user_id 或 rate:tpm:user_id)
-- ARGV[1]: window_size_seconds (窗口大小，通常 60 秒)
-- ARGV[2]: max_requests (允许的最大请求或 Token 数)
-- ARGV[3]: increment (本次增加的值，RPM 为 1，TPM 为预估 token)

local key = KEYS[1]
local window = tonumber(ARGV[1])
local max_req = tonumber(ARGV[2])
local inc = tonumber(ARGV[3])

local current = redis.call("GET", key)
if current and tonumber(current) + inc > max_req then
    return 0 -- 限流触发
end

redis.call("INCRBY", key, inc)
if not current then
    -- 如果是第一次设置，给这个 key 加个过期时间
    redis.call("EXPIRE", key, window)
end

return 1 -- 允许通过
