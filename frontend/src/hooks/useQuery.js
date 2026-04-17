import { useState, useEffect, useCallback, useRef } from 'react'

/* Simple async data fetching hook with loading/error state */
export function useQuery(fetchFn, deps = [], options = {}) {
  const { immediate = true, onError } = options
  const [data, setData]       = useState(null)
  const [loading, setLoading] = useState(immediate)
  const [error, setError]     = useState(null)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  const execute = useCallback(async (...args) => {
    setLoading(true)
    setError(null)
    try {
      const result = await fetchFn(...args)
      if (mountedRef.current) setData(result)
      return result
    } catch (err) {
      if (mountedRef.current) setError(err.message || 'Unknown error')
      onError?.(err)
      return null
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  useEffect(() => {
    if (immediate) execute()
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [execute])

  return { data, loading, error, refetch: execute }
}

/* Mock data generator utilities */
export function generateTimeSeries(days = 30, baseValue = 5000, variance = 0.3) {
  const now = Date.now()
  return Array.from({ length: days }, (_, i) => {
    const ts = now - (days - 1 - i) * 86400000
    const v  = baseValue * (1 + (Math.random() - 0.5) * variance)
    return { timestamp_ms: ts, value: Math.round(v) }
  })
}

export function generateHourlySeries(hours = 24, baseValue = 200) {
  const now = Date.now()
  return Array.from({ length: hours }, (_, i) => {
    const ts = now - (hours - 1 - i) * 3600000
    const v  = baseValue * (1 + (Math.random() - 0.5) * 0.5)
    return { timestamp_ms: ts, value: Math.round(v) }
  })
}
