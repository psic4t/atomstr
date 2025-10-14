FROM golang:1.22-alpine as build

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY *.go ./
COPY static/ ./static/
COPY templates/ ./templates/

RUN apk add --no-cache build-base

#RUN CGO_ENABLED=1 go build -ldflags="-s -w -linkmode external -extldflags '-static'"
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w -linkmode external -extldflags '-static'" -o /atomstr

FROM alpine:latest

LABEL org.opencontainers.image.title="atomstr"
LABEL org.opencontainers.image.source=https://sr.ht/~psic4t/atromstr
LABEL org.opencontainers.image.description="Atomstr is a RSS/Atom gateway to Nostr"
LABEL org.opencontainers.image.authors="psic4t"
LABEL org.opencontainers.image.licenses=GPL

COPY --from=build /atomstr .
COPY --from=build /app/static/ ./static/
COPY --from=build /app/templates/ ./templates/

CMD [ "/atomstr" ]
