PYTHON_VERSION ?= 3.11
PYENV ?= pyenv
POETRY ?= poetry
PYINSTALLER ?= pyinstaller
GIT ?= git
PRINT ?= printf

PYTHON := python$(PYTHON_VERSION)
PACKAGE := jobman
PACKAGE_DIR := $(PACKAGE)/
TEST_DIR := tests/


.PHONY: help
help: ## Show this help.
	@fgrep -h "##" $(MAKEFILE_LIST) | fgrep -v fgrep | sed -e 's/\\$$//' | sed -e 's/##//'

.PHONY: all
all: ## Setup, generate docs, format, test, run and clean. Does *not* publish to PyPI
all: setup fmt test clean changes build

.PHONY: setup
setup: ## Create and install into virtual environment for development.
	$(PYENV) install --skip-existing $(PYTHON_VERSION)
	$(PYENV) local $(PYTHON_VERSION)
	$(POETRY) env use $(PYTHON_VERSION)
	$(POETRY) install --no-interaction

.PHONY: poetrysetup
poetrysetup: ## Set up Poetry
	$(POETRY) install --no-interaction

.PHONY: fmt
fmt: ## Apply auto code formatting.
	$(POETRY) run autoflake --recursive --exclude=__init__.py --in-place \
	--remove-unused-variables --remove-all-unused-imports $(PACKAGE_DIR) $(TEST_DIR)
	$(POETRY) run isort $(PACKAGE_DIR) $(TEST_DIR)
	$(POETRY) run python -m black --preview  $(PACKAGE_DIR) $(TEST_DIR)

.PHONY: typetest
typetest: ## Run type hinting tests.
	$(POETRY) run mypy $(PACKAGE_DIR) $(TEST_DIR)

.PHONY: unittest
unittest: ## Run unit tests and end-to-end tests.
	$(POETRY) run pytest --cov-report term --cov-report html $(TEST_DIR)

.PHONY: test
test: ## Run all tests: type tests, unit tests, and end-to-end tests.
test: typetest unittest

.PHONY: clean
clean: ## Delete runtime files.
	find . -regex '\|build\|.*.mypy_cache\|.*.pytest_cache\|.*__pycache__' \
	! -path './venv/*' ! -path './project_boilerplate/venv/*' -prune -exec rm -rf "{}" \;

# excludes PDF format docs, which frequently contain nonmeaningful changes
# meaningful changes to docs will be reflected in Markdown versions
.PHONY: changes
changes: ## Check for uncommitted changes.
	@$(GIT) status --porcelain=v1 2>/dev/null | grep -q '.*' \
	&& { $(PRINT) "\nFAILED: Uncommitted changes. Changes to docs or formatting?\n"; exit 1; } \
	|| { $(PRINT) "\nSUCCESS: Ready to release.\n"; exit 0; }

.PHONY: build
build: ## Build wheel with poetry and single executable with PyInstaller
	$(POETRY) build
	$(PYINSTALLER) --onefile -n $(PACKAGE) installer/jobman_pyinstaller_wrapper.py

.PHONY: publish
publish: ## Publish to PyPI with poetry
	$(POETRY) publish