# Stage 1 build
FROM node:latest as react-build

WORKDIR /app

# Copy node package files first.
COPY package.json yarn.lock ./
RUN yarn install --verbose

COPY . ./
RUN yarn build

# Stage 2 web
FROM nginx:alpine

COPY --from=react-build /app/build /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY htpasswd /etc/nginx/

EXPOSE 80
