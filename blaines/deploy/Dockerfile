# Serves the static site with nginx, non-root, listening on 8080.
#
# Build from the blaines/ directory (one level up from this file):
#   cd blaines
#   docker build -f deploy/Dockerfile -t blaines-site:latest .
#
# Files are placed under html/blaines/ so the container serves the site at
# /blaines/... with no rewrite rules — the same path the ingress forwards.
# Locally: docker run -p 8080:8080 blaines-site  ->  http://localhost:8080/blaines/

FROM nginxinc/nginx-unprivileged:1.27-alpine

# Replaces the stock server config — see comments in the conf for why
# (directory redirects must not leak the internal 8080 port).
COPY deploy/nginx-default.conf /etc/nginx/conf.d/default.conf

COPY index.html /usr/share/nginx/html/blaines/
COPY css/ /usr/share/nginx/html/blaines/css/
COPY js/  /usr/share/nginx/html/blaines/js/
COPY img/ /usr/share/nginx/html/blaines/img/

# Convenience: hitting the container root redirects to the site.
# (The base image runs as the unprivileged nginx user, which can't write
# to the root-owned html dir — switch to root just for this step.)
USER root
RUN printf '<meta http-equiv="refresh" content="0; url=blaines/">' \
      > /usr/share/nginx/html/index.html
USER nginx

EXPOSE 8080
