# Serves the static site with nginx, non-root, listening on 8080.
#
# Build from the repo root (one level up from this file):
#   docker build -f deploy/Dockerfile -t blaines-site:latest .
#   docker run -p 8080:8080 blaines-site  ->  http://localhost:8080/
#
# Files are served from the web root, so the site lives at / on blaines.shop.

FROM nginxinc/nginx-unprivileged:1.27-alpine

# Replaces the stock server config — see comments in the conf for why
# (directory redirects must not leak the internal 8080 port).
COPY deploy/nginx-default.conf /etc/nginx/conf.d/default.conf

COPY index.html /usr/share/nginx/html/
COPY css/ /usr/share/nginx/html/css/
COPY js/  /usr/share/nginx/html/js/
COPY img/ /usr/share/nginx/html/img/

EXPOSE 8080
