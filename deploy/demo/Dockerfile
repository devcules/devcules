# The demo store: one Go binary, no dependencies, serving a page whose
# Pay button is on a canvas (the desk's agent can only click it via
# the machines helper's grounding, per PLAN.md §11).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY main.go .
RUN go build -o /demo-store main.go

FROM alpine:3.20
RUN apk add --no-cache wget
COPY --from=build /demo-store /demo-store
EXPOSE 8080
USER 10001:10001
ENTRYPOINT ["/demo-store"]
