export GO111MODULE=on

all: base api alarm_rules_manager

version:
	git describe --always --tags > .version

setup:
	go install github.com/swaggo/swag/cmd/swag@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install golang.org/x/tools/cmd/stringer@latest

error_codes_string:
	go generate src/common/error_codes.go
	
routes_docs:
	swag i -g routes.go -dir src/apis,src/common --instanceName v1 -o docs/routes

alarm_rules_manager_docs:
	cd cmds/alarm_rules_manager && swag i -g alarm_rules_manager.go -dir .,../../src/common,../../src/apis --instanceName alarm_v1 -o ../../docs/alarm_rules_manager

docs: routes_docs alarm_rules_manager_docs


base: version
	cd cmds/base && go build -o ../../clbase -ldflags "-X \"main.Version=`cat ../../.version`\""

api: version
	cd cmds/api && go build -o ../../clapi -ldflags "-X \"main.Version=`cat ../../.version`\""

alarm_rules_manager: version
	cd cmds/alarm_rules_manager && CGO_ENABLED=0 go build -o ../../alarm_rules_manager -ldflags "-X \"main.Version=`cat ../../.version`\"" alarm_rules_manager.go

test:
	go test ./...

tidy:
	go mod tidy

deps:
	go build -v ./...

upgrade:
	go get -u

clean:
	rm -rf docs
