FROM golang:1.26.3 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /probe-service ./cmd

# ffprobe comes from a static ffmpeg build; only the ffprobe binary is copied
# into the final image to keep it small (design: multi-stage, ffprobe only).
FROM mwader/static-ffmpeg:7.1 AS ffmpeg

FROM gcr.io/distroless/static-debian12
COPY --from=ffmpeg /ffprobe /usr/local/bin/ffprobe
COPY --from=build /probe-service /app/probe-service

ENV PORT=8080
ENV PATH="/usr/local/bin"
EXPOSE 8080

ENTRYPOINT ["/app/probe-service"]
