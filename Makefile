.PHONY: build test e2e clean

GOBIN := ./auto

build:
	go build -o $(GOBIN) ./cmd/auto

test:
	go test ./...

# e2e drives every CLI command against a live EKS Auto cluster.
# Required:
#   KCTX        kubeconfig context name
#   TARGET_NODE node to drive tests against
# Optional:
#   IMAGE       debug pod image override (default nicolaka/netshoot:latest)
e2e: build
	@if [ -z "$(KCTX)" ] || [ -z "$(TARGET_NODE)" ]; then \
		echo "ERROR: set KCTX=<context> TARGET_NODE=<node>"; exit 1; \
	fi
	AUTO=$(GOBIN) KCTX=$(KCTX) TARGET_NODE=$(TARGET_NODE) test/e2e.sh

clean:
	rm -f $(GOBIN) tcpdump-*.pcap
