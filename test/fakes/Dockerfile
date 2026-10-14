# Build context is the repo root: docker build -f test/fakes/Dockerfile .
FROM golang:1.27-trixie AS build
WORKDIR /src
COPY go.mod go.sum* ./
COPY test/fakes ./test/fakes
RUN CGO_ENABLED=0 go build -trimpath -o /out/fakes ./test/fakes

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/fakes /fakes
EXPOSE 9090
HEALTHCHECK CMD ["/fakes","get","http://127.0.0.1:9090/healthz"]
ENTRYPOINT ["/fakes"]
