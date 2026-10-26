# syntax=docker/dockerfile:1
FROM node:24-alpine AS build

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build && npm prune --omit=dev

FROM node:24-alpine AS runtime

ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=4321

WORKDIR /app
COPY --from=build /app/package.json ./
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist

USER node
EXPOSE 4321
CMD ["node", "./dist/server/entry.mjs"]
