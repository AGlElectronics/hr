# Volunteer signing UI (static) + reverse-proxy of /api to hr-backend.
# Non-root nginx on 8080 (same pattern as hr-frontend).
# Build context: the module root (same as other HR images), for example
#   docker build -f sign-frontend/Dockerfile -t hr-sign .
FROM nginxinc/nginx-unprivileged:1.30.6-alpine
USER root
RUN apk upgrade --no-cache
COPY sign-frontend/index.html /usr/share/nginx/html/index.html
COPY sign-frontend/nginx.conf /etc/nginx/conf.d/default.conf
USER nginx
EXPOSE 8080
