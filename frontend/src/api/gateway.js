import { gatewayGet, gatewayPost } from './client.js'

/* ─── Health ──────────────────────────────────────── */
export const checkGatewayHealth = () => gatewayGet('/health')

/* ─── Event ingest ────────────────────────────────── */
export const ingestEvents = (batch, apiKey) =>
  gatewayPost('/v1/events', batch, apiKey)

export const trackEvent = (event, apiKey) =>
  gatewayPost('/v1/track', event, apiKey)

export const identifyUser = (payload, apiKey) =>
  gatewayPost('/v1/identify', payload, apiKey)
