# Volunteer signing UI (static) + reverse-proxy of /api to hr-backend.
# Non-root nginx on 8080 (same pattern as hr-frontend).
FROM nginxinc/nginx-unprivileged:1.30.6-alpine
USER root
RUN apk upgrade --no-cache
COPY index.html /usr/share/nginx/html/index.html
COPY nginx.conf /etc/nginx/conf.d/default.conf
USER nginx
EXPOSE 8080
