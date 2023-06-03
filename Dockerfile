FROM ubuntu:22.04

WORKDIR /app

RUN apt update -y && \
    apt install -y \
    libgraph-easy-perl # converts graphs in dot format to ascii
COPY ./bin/terradep .

ENTRYPOINT ["./terradep"]

# expects to have mounted:
# - analyzed directory in /work/analyze
# - output file in /work/output.dot (it must exist before mounting)
CMD ["/work/analyze", "/work/output.dot"]