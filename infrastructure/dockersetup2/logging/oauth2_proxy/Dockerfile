FROM golang:latest

RUN go get -v github.com/bitly/oauth2_proxy && \
    cd $GOPATH/src/github.com/bitly/oauth2_proxy

RUN cd $GOPATH/src/github.com/bitly/oauth2_proxy && \
    git remote rename origin upstream && \
    git remote add origin https://github.com/JacobAae/oauth2_proxy && \
    git fetch origin && \
    git reset --hard origin/master && \
    go get -v .

WORKDIR /go/bin
CMD oauth2_proxy
