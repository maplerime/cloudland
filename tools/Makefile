VENV_DIR := .venv
PYTHON   := $(VENV_DIR)/bin/python
PIP      := $(VENV_DIR)/bin/pip
CLI      := $(VENV_DIR)/bin/cloudland

.PHONY: all install check-deps clean help

all: install

check-deps:
	@command -v python3 >/dev/null 2>&1 || { echo "Error: python3 is not installed"; exit 1; }
	@python3 -c "import venv" >/dev/null 2>&1 || { echo "Error: python3-venv is not installed. Install it with: sudo apt install python3-venv"; exit 1; }
	@python3 -c "import pip" >/dev/null 2>&1  || { echo "Error: python3-pip is not installed. Install it with: sudo apt install python3-pip"; exit 1; }

$(VENV_DIR): check-deps
	python3 -m venv $(VENV_DIR)
	$(PIP) install --upgrade pip

install: $(VENV_DIR)
	$(PIP) install -e .
	@echo ""
	@echo "Installation complete. Usage:"
	@echo "  $(CLI) --help"
	@echo ""
	@echo "Or activate the venv first:"
	@echo "  source $(VENV_DIR)/bin/activate"
	@echo "  cloudland --help"

clean:
	rm -rf $(VENV_DIR)
	rm -rf *.egg-info
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true

help:
	@echo "Usage:"
	@echo "  make install  - Create venv and install cloudland CLI"
	@echo "  make clean    - Remove venv and build artifacts"
