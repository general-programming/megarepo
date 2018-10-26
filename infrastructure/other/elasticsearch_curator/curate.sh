docker build -t elastic_curator --force-rm .
docker run --rm elastic_curator --config=/config.yml "/$1"
