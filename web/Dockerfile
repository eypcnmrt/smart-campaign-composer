# syntax=docker/dockerfile:1
#
# The web app: built with a pinned node, served by a pinned nginx that does not run as root and listens on 8080. The same
# container forwards /api to the API container (nginx/default.conf), so the browser only ever talks to its own origin.
#
# Type checking and the tests run in `make test` (they need the shared fixtures of the backend, which are not part of this build
# context); this build only has to produce the files.

FROM node:24.14.0-alpine AS build
WORKDIR /app
# No package may run an install script while the image is built (the same rule as scripts/web.sh).
ENV npm_config_ignore_scripts=true
COPY package.json package-lock.json .npmrc ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY index.html vite.config.ts tsconfig.json tsconfig.node.json ./
COPY public ./public
COPY src ./src
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.30.6-alpine
COPY nginx/default.conf /etc/nginx/conf.d/default.conf
COPY nginx/security-headers.conf /etc/nginx/snippets/security-headers.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
