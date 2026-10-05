NAME := token-usage-tree
OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
MACH := $(shell uname -m)
VENV := .buildvenv
PY := $(VENV)/bin/python

ifeq ($(MACH),x86_64)
  ARCH := amd64
else ifeq ($(MACH),aarch64)
  ARCH := arm64
else
  ARCH := $(MACH)
endif
PLATFORM := $(OS)-$(ARCH)

.PHONY: build test clean

build: $(PY)
	$(PY) -m PyInstaller --onefile --clean \
		--name $(NAME)-$(PLATFORM) \
		--add-data $(CURDIR)/src/index.html:. \
		--add-data $(CURDIR)/src/i18n.js:. \
		--distpath dist --workpath build --specpath build \
		dashboard.py
	@echo "Built dist/$(NAME)-$(PLATFORM)"

$(PY):
	python3 -m venv $(VENV)
	$(PY) -m pip install --quiet --upgrade pyinstaller

test:
	python3 test/test_dashboard.py
	node --test test/filter.test.mjs test/i18n.test.mjs test/refresh.test.mjs test/dbfield.test.mjs

clean:
	rm -rf dist build $(VENV) *.spec
