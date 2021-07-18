FROM golang:1.16-alpine AS gmc_builder

RUN go env -w GO111MODULE=auto \
  && go env -w CGO_ENABLED=0 \
  && mkdir /build

WORKDIR /build

COPY ./ .

RUN cd /build \
  && go build -ldflags "-s -w -extldflags '-static'" -o gmc


FROM node:latest AS ui_builder

WORKDIR /build

RUN cd /build \
  && git clone https://github.com/ProtobufBot/Client-UI.git \
  && cd /build/Client-UI \
  && npm install \
  && npm run build

FROM alpine:latest

WORKDIR /data

COPY --from=gmc_builder /build/gmc /usr/bin/gmc
RUN chmod +x /usr/bin/gmc

COPY --from=ui_builder /build/Client-UI/dist /data/static

ADD ./scripts/env_run.sh /data/

RUN chmod +x /data/env_run.sh
EXPOSE 9000
CMD /data/env_run.sh