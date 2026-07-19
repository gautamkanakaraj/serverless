# 🤝 Contributing to Mini-Lambda

First off, thank you for considering contributing to Mini-Lambda! It's people like you who make open-source software a great ecosystem for everyone.

---

## 🛠️ Local Development Setup

To get started on development, follow these steps:

1. **Fork and Clone the Repository:**
   ```bash
   git clone https://github.com/YOUR-USERNAME/serverless.git
   cd serverless
   ```

2. **Configure Environment Variables:**
   Copy the example environment template:
   ```bash
   cp .env.example .env
   ```
   Open `.env` and fill in your test credentials (such as a local PostgreSQL connection or a Neon development database connection, and Google OAuth test credentials if testing sign-in).

3. **Verify Prerequisites:**
   * Go `v1.20` or higher.
   * Python `v3.x` (required for running Python sandboxing tests).

4. **Start the Control Plane:**
   ```bash
   go run control-plane/cmd/server/main.go
   ```
   Open `http://localhost:8080` in your web browser.

---

## 🧪 Testing Guidelines

Before submitting any code changes, please make sure all tests pass:

1. **Run the Go Unit Tests:**
   Ensure that the database, router, and sandboxed execution engines compile and pass tests:
   ```bash
   go test -v ./...
   ```

2. **Formatting Code:**
   Keep code formatted according to standard Go styling guidelines:
   ```bash
   go fmt ./...
   ```

---

## 📥 Pull Request Workflow

1. Create a new branch for your feature or bug fix:
   ```bash
   git checkout -b feature/your-feature-name
   ```
2. Make your changes and commit them with descriptive commit messages.
3. Push the branch to your fork:
   ```bash
   git push origin feature/your-feature-name
   ```
4. Open a Pull Request (PR) on the main repository and describe your changes. We will review it as soon as possible!
