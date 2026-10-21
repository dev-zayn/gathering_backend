# The docs are static files: built here, served by nginx, nothing running at
# request time. The one address a developer is given before they have a game
# should not depend on a process that can be down.
#
# Two builds come out of one tree: the public site at the root, and the
# app section under /app/, which nginx puts behind a password.

FROM node:22-alpine AS build
WORKDIR /w
COPY package.json package-lock.json ./
RUN npm ci
COPY . ./
RUN npm run build

FROM nginx:1.30-alpine
RUN apk add --no-cache openssl
COPY --from=build /w/dist /usr/share/nginx/html
COPY --from=build /w/dist-app /usr/share/nginx/private/app
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY docker/docs-auth.sh /docker-entrypoint.d/40-docs-auth.sh
RUN chmod +x /docker-entrypoint.d/40-docs-auth.sh
