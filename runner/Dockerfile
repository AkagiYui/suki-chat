# suki-runner —— 每会话容器内的 agent 运行时（pi）。
# 浏览器/截图工具在后续阶段加入（Chromium）。
FROM node:lts-alpine

WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev

COPY server.mjs ./
RUN mkdir -p /workspace

EXPOSE 8088
CMD ["node", "/app/server.mjs"]
