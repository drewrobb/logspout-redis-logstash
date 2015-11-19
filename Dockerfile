FROM gliderlabs/alpine:latest
ENTRYPOINT ["/bin/logspout"]
VOLUME /mnt/routes
EXPOSE 8000

ENV GOPATH /go
RUN apk-install go git mercurial
COPY logspout /go/src/github.com/gliderlabs/logspout

RUN mkdir -p /go/src/github.com/strava/logspout-redis-logstash
ADD redis.go /go/src/github.com/strava/logspout-redis-logstash
RUN find -name modules.go | xargs rm
COPY ./modules.go /go/src/github.com/gliderlabs/logspout/modules.go

WORKDIR /go/src/github.com/gliderlabs/logspout

RUN cd /go/src/github.com/gliderlabs/logspout && go get && go build -ldflags "-X main.Version dev" -o /bin/logspout
