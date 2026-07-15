# @author hanchao <hanchao@66yunlian.com>
# @since 0.1.0
FROM mcr.microsoft.com/playwright:v1.61.0-noble@sha256:57b65fdc9ceabe0ef613124c7bbe2babcf9362c4d85e382fe3b03604e84b428a

ENV CI=1 \
    PLAYWRIGHT_HTML_OPEN=never

WORKDIR /workspace/web

COPY web/package.json web/package-lock.json ./
RUN test "$(npm --version)" = "11.13.0" && npm ci

COPY web/index.html web/*.json web/*.ts web/*.js ./
COPY web/src ./src
COPY web/e2e ./e2e

CMD ["npm", "run", "test:e2e"]
