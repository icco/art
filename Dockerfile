FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/art-server .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/art-server /art-server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/art-server"]
