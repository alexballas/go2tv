FROM golang:1.26

ARG DEBIAN_FRONTEND=noninteractive
RUN \
  apt-get update && \
  apt-get install --no-install-recommends -y xorg-dev libwayland-dev libxkbcommon-dev libegl-dev libpipewire-0.3-dev pkg-config && \
  apt-get clean && \
  rm -rf /var/lib/apt/lists/*

WORKDIR /usr/local/src/go2tv/
COPY . .

ENV GODEBUG=asyncpreemptoff=1
RUN make
RUN make install

ENTRYPOINT [ "go2tv" ]
