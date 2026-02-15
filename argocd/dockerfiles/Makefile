update:
	docker buildx build \
		-f librenms/Dockerfile \
		--push \
		--platform linux/amd64,linux/arm64 \
		-t nepeat/librenms:latest \
		librenms
