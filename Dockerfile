# Builds tallybook-mcp, the MCP server, for registries such as Glama that
# introspect a server by running its container. The server speaks MCP on
# stdio and reads transcripts from $HOME/.claude and $HOME/.codex, so mount
# them read-only to see real data:
#
#   docker run -i --rm \
#     -v ~/.claude:/data/.claude:ro -v ~/.codex:/data/.codex:ro \
#     tallybook-mcp
#
# With nothing mounted the server starts with an empty ledger, which is
# enough for tools/list and for every tool to answer "no sessions".

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tallybook-mcp ./cmd/tallybook-mcp

FROM alpine:3.22
RUN adduser -D -h /data tallybook
COPY --from=build /out/tallybook-mcp /usr/local/bin/tallybook-mcp
USER tallybook
ENV HOME=/data
WORKDIR /data
ENTRYPOINT ["tallybook-mcp"]
