FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/cfasuite-hr .

FROM alpine:3.21
WORKDIR /app
ENV CFASUITE_ADDR=:8217
ENV CFASUITE_DB_PATH=/app/data/cfasuite-hr.db
RUN adduser -D -h /app cfasuite && mkdir -p /app/data && chown -R cfasuite:cfasuite /app
COPY --from=build /out/cfasuite-hr /usr/local/bin/cfasuite-hr
USER cfasuite
EXPOSE 8217
VOLUME ["/app/data"]
CMD ["cfasuite-hr", "serve"]
