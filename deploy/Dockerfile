FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY kubepilot /usr/local/bin/kubepilot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kubepilot"]
