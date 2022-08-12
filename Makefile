IMAGE_NAME := "webhook"
IMAGE_TAG := "latest"

OUT := $(shell pwd)/_out
BINARY := webhook

KUBE_VERSION=1.24.2
OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)

$(shell mkdir -p "$(OUT)")
export TEST_ASSET_ETCD=_test/kubebuilder/bin/etcd
export TEST_ASSET_KUBE_APISERVER=_test/kubebuilder/bin/kube-apiserver
export TEST_ASSET_KUBECTL=_test/kubebuilder/bin/kubectl

build:
	docker build -t "$(IMAGE_NAME):$(IMAGE_TAG)" .

release:
	docker buildx build -t "$(IMAGE_NAME):$(IMAGE_TAG)" --platform=linux/amd64,linux/arm64 --push .

.PHONY: rendered-manifest.yaml
rendered-manifest.yaml:
	helm template \
			--name example-webhook \
				--set image.repository=$(IMAGE_NAME) \
				--set image.tag=$(IMAGE_TAG) \
				deploy/example-webhook > "$(OUT)/rendered-manifest.yaml"

test: _test/kubebuilder
	go test -v .

_test/kubebuilder:
	curl -fsSL https://go.kubebuilder.io/test-tools/$(KUBE_VERSION)/$(OS)/$(ARCH) -o kubebuilder-tools.tar.gz
	mkdir -p _test/kubebuilder
	tar -xvf kubebuilder-tools.tar.gz
	mv kubebuilder/bin _test/kubebuilder/
	rm kubebuilder-tools.tar.gz
	rm -R kubebuilder

clean-kubebuilder:
	rm -Rf _test/kubebuilder

clean: clean-kubebuilder
