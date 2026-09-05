package controlplane

const openAPIDocument = `{
  "openapi": "3.0.3",
  "info": {"title": "Go Serve Control Plane API", "version": "1.0.0"},
  "paths": {
    "/health": {"get": {"operationId": "health"}},
    "/ready": {"get": {"operationId": "readiness"}},
    "/api/v1/config": {"get": {"operationId": "getConfig"}, "put": {"operationId": "replaceConfig"}},
    "/api/v1/config/validate": {"post": {"operationId": "validateConfig"}},
    "/api/v1/config/rollback": {"post": {"operationId": "rollbackConfig"}},
    "/api/v1/config/status": {"get": {"operationId": "getConfigStatus"}},
    "/api/v1/servers": {"get": {"operationId": "listServers"}},
    "/api/v1/routes": {"get": {"operationId": "listRoutes"}}
  }
}`

const swaggerUIHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Go Serve API</title>
<style>body{font-family:system-ui,sans-serif;max-width:56rem;margin:3rem auto;padding:0 1rem;color:#18212b}code{background:#eef2f5;padding:.15rem .3rem}</style></head>
<body><h1>Go Serve Control Plane</h1><p>OpenAPI document: <a href="/api/openapi.json"><code>/api/openapi.json</code></a></p></body>
</html>`
