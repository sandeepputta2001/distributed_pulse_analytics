import { queryGet, queryPost, queryPut, queryDelete, authPost } from './client.js'

/* Health */
export const checkHealth = () => queryGet('/health')

/* Event counts */
export const getEventCount = (params) => queryGet('/v1/events/count', params)
// params: { app_id, event_name?, from_ms?, to_ms?, granularity? }

/* Active Users (DAU/WAU/MAU) */
export const getActiveUsers = (params) => queryGet('/v1/dau', params)
// params: { app_id, from_ms?, to_ms?, granularity? }

/* Funnels */
export const queryFunnel   = (body)   => queryPost('/v1/funnels/query', body)
export const createFunnel  = (body)   => queryPost('/v1/funnels', body)
export const listFunnels   = (app_id) => queryGet(`/v1/apps/${app_id}/funnels`)

/* Retention */
export const getRetention = (body) => queryPost('/v1/retention', body)

/* Session metrics */
export const getSessionMetrics = (params) => queryGet('/v1/sessions/metrics', params)
// params: { app_id, from_ms?, to_ms? }

/* Alert rules */
export const listAlerts   = (app_id)   => queryGet('/v1/alerts', { app_id })
export const createAlert  = (body)     => queryPost('/v1/alerts', body)
export const updateAlert  = (id, body) => queryPut(`/v1/alerts/${id}`, body)
export const deleteAlert  = (id)       => queryDelete(`/v1/alerts/${id}`)

/* Experiments */
export const listExperiments   = (app_id)   => queryGet('/v1/experiments', { app_id })
export const createExperiment  = (body)     => queryPost('/v1/experiments', body)
export const updateExperiment  = (id, body) => queryPut(`/v1/experiments/${id}`, body)
export const deleteExperiment  = (id)       => queryDelete(`/v1/experiments/${id}`)

/* Cohorts */
export const listCohorts     = (app_id) => queryGet('/v1/cohorts', { app_id })
export const createCohort    = (body)   => queryPost('/v1/cohorts', body)
export const deleteCohort    = (id)     => queryDelete(`/v1/cohorts/${id}`)
export const recomputeCohort = (id)     => queryPost(`/v1/cohorts/${id}/recompute`, {})

/* Apps */
export const listApps  = ()            => queryGet('/v1/apps')
export const getApp    = (id)          => queryGet(`/v1/apps/${id}`)
export const createApp = (body)        => queryPost('/v1/apps', body)
export const updateApp = (id, body)    => queryPut(`/v1/apps/${id}`, body)
export const deleteApp = (id)          => queryDelete(`/v1/apps/${id}`)

/* Orgs */
export const listOrgs  = ()            => queryGet('/v1/orgs')
export const createOrg = (body)        => queryPost('/v1/orgs', body)
export const updateOrg = (id, body)    => queryPut(`/v1/orgs/${id}`, body)

/* Auth service (proxied via /api/auth -> :8083) */
export const register     = (body)          => authPost('/v1/auth/register', body)
export const rotateApiKey = (appId, token)  => authPost('/v1/auth/apikey/rotate', { app_id: appId }, token)
