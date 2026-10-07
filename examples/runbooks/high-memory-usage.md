---
name: High Memory Usage
match:
  alertname: HighMemoryUsage
  severity: critical
tags: [memory, oom, kubernetes]
---
## Symptoms
Pod memory usage exceeds 90% of its limit.

## Common Causes
1. Memory leak in the application.
2. Memory limit too low for the current load.
3. Unbounded in-process cache.

## Investigation Steps
1. Query the trend of `container_memory_working_set_bytes` for the pod over the last hour.
2. Check pod events for `OOMKilled` and recent restarts.
3. Check whether a deployment rolled out shortly before the alert.

## Remediation
- Short term: raise the memory limit or scale out.
- Long term: profile application memory and bound caches.
