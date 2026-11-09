FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/docpulse-server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/docpulse-server ./docpulse-server
COPY docpulse.yaml ./docpulse.yaml
COPY README.md ./README.md
EXPOSE 8080
ENTRYPOINT ["./docpulse-server"]
