build:
	docker compose pull
	docker pull golang:1.19
	docker compose build --force-rm

deploy: build
	docker compose up -d

logs:
	docker compose logs -f
