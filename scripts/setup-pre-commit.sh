#!/bin/bash

# Setup script for pre-commit hooks and development environment

set -e

echo "🚀 Setting up KubeFTPd development environment..."

# Check if pre-commit is installed
if ! command -v pre-commit &> /dev/null; then
    echo "📦 Installing pre-commit..."
    if command -v pip &> /dev/null; then
        pip install pre-commit
    elif command -v pip3 &> /dev/null; then
        pip3 install pre-commit
    else
        echo "❌ Error: pip or pip3 not found. Please install Python and pip first."
        echo "   You can install pre-commit with: pip install pre-commit"
        exit 1
    fi
else
    echo "✅ pre-commit is already installed"
fi

# Install pre-commit hooks
echo "🔗 Installing pre-commit hooks..."
pre-commit install
pre-commit install --hook-type commit-msg

# Install Go tools
echo "🛠️  Installing Go development tools..."
make golangci-lint
make gosec

# Run pre-commit once to set everything up
echo "🧪 Running pre-commit for the first time..."
if ! pre-commit run --all-files; then
    echo "⚠️  Some pre-commit checks failed. This is normal for first setup."
    echo "   The hooks are now installed and will run on your next commit."
else
    echo "✅ All pre-commit checks passed!"
fi

echo ""
echo "🎉 Development environment setup complete!"
echo ""
echo "Available make targets:"
echo "  make help           - Show all available targets"
echo "  make pre-commit     - Run all pre-commit hooks"
echo "  make test-coverage  - Run tests with coverage"
echo "  make lint           - Run Go linter"
echo "  make security-scan  - Run security scanner"
echo ""
echo "The pre-commit hooks will now run automatically before each commit."
echo "To manually run them: make pre-commit"