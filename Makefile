UNAME_S := $(shell uname -s)

ifeq ($(findstring MINGW,$(UNAME_S)),MINGW)
  OS := windows
else ifeq ($(findstring MSYS,$(UNAME_S)),MSYS)
  OS := windows
else ifeq ($(findstring CYGWIN,$(UNAME_S)),CYGWIN)
  OS := windows
else
  OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
endif

VENV := .buildvenv

ifeq ($(OS),windows)
  PYTHON ?= python
  PY := $(VENV)/Scripts/python.exe
  DATASEP := ;
else
  PYTHON ?= python3
  PY := $(VENV)/bin/python
  DATASEP := :
endif

PYINSTALL := $(PY) -m PyInstaller --onefile --clean \
	--add-data "$(CURDIR)/src/index.html$(DATASEP)." \
	--add-data "$(CURDIR)/src/i18n.js$(DATASEP)." \
	--distpath dist --workpath build --specpath build

.PHONY: build build-all build-linux build-macos build-windows test clean

build build-all: build-linux build-macos build-windows

build-linux: $(PY)
ifeq ($(OS),linux)
	$(PYINSTALL) --name dashboard-lin-x64 dashboard.py
	@echo "Built dist/dashboard-lin-x64"
else
	@echo "Skipping build-linux on $(OS)"
endif

build-macos: $(PY)
ifeq ($(OS),darwin)
	$(PYINSTALL) --name dashboard-mac-darwin dashboard.py
	@echo "Built dist/dashboard-mac-darwin"
else
	@echo "Skipping build-macos on $(OS)"
endif

build-windows: $(PY)
ifeq ($(OS),windows)
	$(PYINSTALL) --name dashboard dashboard.py
	@echo "Built dist/dashboard.exe"
else
	@echo "Skipping build-windows on $(OS)"
endif

$(PY):
	$(PYTHON) -m venv $(VENV)
	$(PY) -m pip install --quiet --upgrade pyinstaller

test:
	python3 test/test_dashboard.py
	node --test test/filter.test.mjs test/i18n.test.mjs test/refresh.test.mjs test/dbfield.test.mjs

clean:
	rm -rf dist build $(VENV) *.spec
