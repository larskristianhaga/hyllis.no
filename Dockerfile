FROM golang:1.26-alpine AS builder

WORKDIR /usr/src/app

COPY go.mod go.sum ./

RUN go mod download && go mod verify

COPY . .

RUN go build -v -o /run-app .

FROM gcr.io/distroless/static-debian12

EXPOSE 8080

COPY --from=builder /run-app /usr/local/bin/
CMD ["run-app"]
