APP=callback_test_server

.PHONY: all build run run-verbose run-port test clean help

# Default target
all: build

# Build test server
build:
	go build -o $(APP) callback_test_server.go

# Run test server (default config)
run: build
	./$(APP)

# Run test server (verbose mode)
run-verbose: build
	./$(APP) -verbose

# Run test server (custom port)
run-port: build
	./$(APP) -port 8081

# Run test script
test:
	bash test_callback.sh

# Clean build artifacts
clean:
	rm -f $(APP)

# Show help
help:
	@echo "Targets:"
	@echo "  build       Build test server binary"
	@echo "  run         Run server on default host/port"
	@echo "  run-verbose Run server in verbose mode"
	@echo "  run-port    Run server on port 8081"
	@echo "  test        Run callback test script"
	@echo "  clean       Remove generated binary"
