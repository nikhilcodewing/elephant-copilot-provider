.PHONY: proto clean

proto:
	@echo "Generating protobuf files..."
	@mkdir -p pb
	protoc --go_out=pb --go_opt=paths=source_relative *.proto
	@echo "Done!"

clean:
	rm -rf pb/

rebuild: clean proto
