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
    ,"/api/v1/cluster/status": {"get": {"operationId": "getClusterStatus"}}
    ,"/api/v1/cluster/members": {"get": {"operationId": "listClusterMembers"}, "post": {"operationId": "addClusterMember"}}
    ,"/api/v1/cluster/members/{id}": {"delete": {"operationId": "removeClusterMember"}}
    ,"/api/v1/cluster/sync": {"get": {"operationId": "getClusterSync"}}
    ,"/api/v1/cluster/leadership/transfer": {"post": {"operationId": "transferClusterLeadership"}}
  }
}`

const swaggerUIHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Go Serve API</title>
<style>body{font-family:system-ui,sans-serif;max-width:64rem;margin:3rem auto;padding:0 1rem;color:#18212b}code,pre{background:#eef2f5;padding:.15rem .3rem}pre{padding:1rem;overflow:auto}</style></head>
<body><h1>Go Serve Control Plane</h1><p>OpenAPI document: <a href="/api/openapi.json"><code>/api/openapi.json</code></a></p><pre id="spec">Loading API specification...</pre>
<script>fetch('/api/openapi.json').then(function(r){return r.json()}).then(function(spec){document.getElementById('spec').textContent=JSON.stringify(spec,null,2)}).catch(function(){document.getElementById('spec').textContent='Unable to load API specification'})</script></body>
</html>`
