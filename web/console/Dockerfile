# The console is static files; the build context is web/console.
FROM node:22-alpine AS build
WORKDIR /w
COPY package.json package-lock.json ./
RUN npm ci
COPY . ./
ARG VITE_API_URL
ENV VITE_API_URL=$VITE_API_URL
# The identity provider: where the console signs in, and as which client.
# Neither is a secret; both are in every sign-in address the browser shows.
ARG VITE_SSO_ISSUER
ENV VITE_SSO_ISSUER=$VITE_SSO_ISSUER
ARG VITE_SSO_CLIENT_ID
ENV VITE_SSO_CLIENT_ID=$VITE_SSO_CLIENT_ID
ARG VITE_SSO_SCOPE
ENV VITE_SSO_SCOPE=$VITE_SSO_SCOPE
# The password form, for a console with no provider: the api checks it
# against CONSOLE_PASSWORD_HASH.
ARG VITE_PASSWORD_SIGNIN
ENV VITE_PASSWORD_SIGNIN=$VITE_PASSWORD_SIGNIN
# The laptop's dev sign-in button. Never on in production.
ARG VITE_DEV_ADMIN
ENV VITE_DEV_ADMIN=$VITE_DEV_ADMIN
RUN npm run build

FROM nginx:1.30-alpine
COPY --from=build /w/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
ARG VITE_API_URL
ARG VITE_SSO_ISSUER
RUN sed -i -e "s|__API_ORIGIN__|${VITE_API_URL}|g" -e "s|__SSO_ORIGIN__|${VITE_SSO_ISSUER}|g" /etc/nginx/conf.d/default.conf \
    && nginx -t
