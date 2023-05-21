FROM ubuntu:22.04

WORKDIR /app

RUN apt install libgraph-easy-perl # converts graphs in dot format to ascii
COPY ./bin/terradep .

ENTRYPOINT [""]
