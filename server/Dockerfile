FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /talkie-server .

FROM alpine:3.22
RUN apk add --no-cache tzdata && adduser -D -H -u 10001 talkie && mkdir /data && chown talkie /data
COPY --from=build /talkie-server /usr/local/bin/talkie-server
USER talkie
ENV TALKIE_DATA=/data TALKIE_ADDR=:7777
VOLUME /data
EXPOSE 7777/udp
ENTRYPOINT ["talkie-server"]
