PREFIX  ?= /usr/local

atomstr: clean
	GOOS=linux GOARCH=amd64 go build -o atomstr -ldflags="-s -w -extldflags=-static"

linux-arm:
	GOOS=linux GOARCH=arm go build -o atomstr -ldflags="-s -w -extldflags=-static"

clean:
	rm -f atomstr

install: 
	install -d $(PREFIX)/bin/
	install -m 755 atomstr $(PREFIX)/bin/atomstr

uninstall:
	rm -f $(PREFIX)/bin/atomstr
