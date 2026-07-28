# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/binapi ./cmd/binapi

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/binapi /bin/binapi
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/bin/binapi"]
