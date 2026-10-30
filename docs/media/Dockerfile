# Recording image: VHS plus the Go toolchain, curl and the vukem CLI.
FROM ghcr.io/charmbracelet/vhs:v0.12.1@sha256:ea49a6a1c529be83153e88321892b5585964418f0b9055e8c1e0d732194234a3
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
	&& rm -rf /var/lib/apt/lists/*
ADD https://go.dev/dl/go1.27.3.linux-amd64.tar.gz /tmp/go.tgz
RUN tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH
COPY vukem /usr/local/bin/vukem
