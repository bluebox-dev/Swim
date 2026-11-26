# Everything a machine's agent (and SWIM WinPE) needs. Included by the internal server block
# and by the public one (public-host.conf), so both always serve agents the same way.

    location = /v1/agent/enroll {
        limit_req zone=swim_auth burst=40 nodelay;   # a room of machines enrolling at once is fine
        limit_req_status 429;
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
    }

    # Agent remote tunnel: an upgraded, long-lived byte stream (api/tunnel.go)
    location ^~ /v1/agent/tunnel/ {
        proxy_pass http://127.0.0.1:8090;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_buffering off;
        proxy_read_timeout 1h;
        proxy_send_timeout 1h;
        access_log off;
    }

    # agent and WinPE API (host token / per-job token on every request)
    location ^~ /v1/agent/ {
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_read_timeout 90s;   # long-poll /agent/commands?wait=55
        client_max_body_size 600m; # driver upload for the per-machine WinPE
    }
    location ^~ /v1/pe/ {
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_read_timeout 90s;
    }

    # Large files: backend authorises, then answers with X-Accel-Redirect: /storage/...
    location ^~ /storage/ {
        internal;
        alias /storage/;
        limit_conn swim_downloads 4;
        limit_conn_status 503;
        limit_rate 40m;              # bytes/s per download (~320 Mbit/s); 4 × = ~1.3 Gbit/s at most
        sendfile on;
        tcp_nopush on;
    }

    # Public agent downloads: binaries, latest.json, install.ps1. Nothing secret lives here —
    # the enrollment token is in the admin-generated command, never in these files.
    location ^~ /dl/agent/ {
        alias /storage/agent/;
        include /etc/nginx/conf.d/security-headers.inc;
        add_header Cache-Control "no-cache";
        default_type application/octet-stream;
        types { application/json json; text/plain ps1; }
    }

