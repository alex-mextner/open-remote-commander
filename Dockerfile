# syntax=docker/dockerfile:1.7
FROM golang:1.27.1 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/orc-server ./cmd/orc-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/orc-server /usr/local/bin/orc-server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/orc-server"]
