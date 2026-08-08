.PHONY: applevm-assets
.PHONY: applevm-smoke
.PHONY: applevm-run
.PHONY: applevm-clean

applevm-assets:
	./hack/applevm/prepare-assets.sh

applevm-smoke:
	mkdir -p bin
	CGO_ENABLED=1 go build \
		-o ./bin/applevm-smoke \
		./cmd/applevm-smoke

	codesign \
		--force \
		--sign - \
		--entitlements ./build/applevm.entitlements \
		./bin/applevm-smoke

applevm-run: applevm-assets applevm-smoke
	./bin/applevm-smoke \
		-kernel ./.local/applevm/vmlinux \
		-disk ./.local/applevm/rootfs.img \
		-cpus 2 \
		-memory 512

applevm-clean:
	rm -rf ./.local/applevm
	rm -f ./bin/applevm-smoke
