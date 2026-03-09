build:
	go build -o ghwatch

install: build
	cp -f ghwatch $(HOME)/.bin/ghwatch
	chmod +x $(HOME)/.bin/ghwatch

clean:
	rm -f ghwatch
