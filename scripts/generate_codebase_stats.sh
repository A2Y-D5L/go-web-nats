#!/usr/bin/env bash
#
# generate_codebase_stats.sh - Generate codebase statistics report
#
# Usage: ./scripts/generate_codebase_stats.sh
#
# Outputs: docs/codebase_stats.md

set -euo pipefail

cd "$(dirname "$0")/.."

OUTPUT_FILE="docs/codebase_stats.md"

# --- Gather Statistics ---

# File counts
GO_FILES=$(find . -name "*.go" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
GO_PROD_FILES=$(find . -name "*.go" ! -name "*_test.go" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
GO_TEST_FILES=$(find . -name "*_test.go" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
JS_FILES=$(find . -name "*.js" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
HTML_FILES=$(find . -name "*.html" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
CSS_FILES=$(find . -name "*.css" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
MD_FILES=$(find . -name "*.md" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
YAML_FILES=$(find . -name "*.yaml" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
JSON_FILES=$(find . -name "*.json" ! -path "./.*" ! -path "./data/*" | wc -l | tr -d ' ')
TOTAL_FILES=$((GO_FILES + JS_FILES + HTML_FILES + CSS_FILES + MD_FILES + YAML_FILES + JSON_FILES))

# Line counts
GO_PROD_LINES=$(find . -name "*.go" ! -name "*_test.go" ! -path "./.*" ! -path "./data/*" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
GO_TEST_LINES=$(find . -name "*_test.go" ! -path "./.*" ! -path "./data/*" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
JS_LINES=$(find . -name "*.js" ! -path "./.*" ! -path "./data/*" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
HTML_LINES=$(find . -name "*.html" ! -path "./.*" ! -path "./data/*" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
CSS_LINES=$(find . -name "*.css" ! -path "./.*" ! -path "./data/*" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
MD_LINES=$(find . -name "*.md" ! -path "./.*" ! -path "./data/*" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
YAML_LINES=$(find . -name "*.yaml" ! -path "./.*" ! -path "./data/*" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
JSON_LINES=$(find . -name "*.json" ! -path "./.*" ! -path "./data/*" -exec cat {} + 2>/dev/null | wc -l | tr -d ' ')
TOTAL_LINES=$((GO_PROD_LINES + GO_TEST_LINES + JS_LINES + HTML_LINES + CSS_LINES + MD_LINES + YAML_LINES + JSON_LINES))

# Go code analysis
GO_FUNCTIONS=$(find . -name "*.go" ! -path "./.*" ! -path "./data/*" -exec grep "^func " {} \; 2>/dev/null | wc -l | tr -d ' ')
GO_STRUCTS=$(find . -name "*.go" ! -path "./.*" ! -path "./data/*" -exec grep "^type.*struct" {} \; 2>/dev/null | wc -l | tr -d ' ')
GO_INTERFACES=$(find . -name "*.go" ! -path "./.*" ! -path "./data/*" -exec grep "^type.*interface" {} \; 2>/dev/null | wc -l | tr -d ' ')
GO_TEST_FUNCS=$(find . -name "*_test.go" ! -path "./.*" ! -path "./data/*" -exec grep "^func Test" {} \; 2>/dev/null | wc -l | tr -d ' ')

# JavaScript functions
JS_FUNCTIONS=$(find . -name "*.js" ! -path "./.*" ! -path "./data/*" -exec grep -E "function |const .* = \(|const .* = async|=> \{" {} \; 2>/dev/null | wc -l | tr -d ' ')

# Dependencies
DIRECT_DEPS=$(awk '/^require \(/,/^\)/' go.mod | grep -v "^require" | grep -v "^)" | grep -v "// indirect" | wc -l | tr -d ' ')
INDIRECT_DEPS=$(awk '/^require \(/,/^\)/' go.mod | grep "// indirect" | wc -l | tr -d ' ')
TOTAL_DEPS=$((DIRECT_DEPS + INDIRECT_DEPS))

# Go version
GO_VERSION=$(grep "^go " go.mod | awk '{print $2}')

# Git stats
GIT_COMMITS=$(git log --oneline 2>/dev/null | wc -l | tr -d ' ')
GIT_CONTRIBUTORS=$(git log --format='%aN' 2>/dev/null | sort -u | wc -l | tr -d ' ')
GIT_COMMITS_THIS_YEAR=$(git log --since="$(date +%Y)-01-01" --oneline 2>/dev/null | wc -l | tr -d ' ')

# Test coverage ratio
if [ "$GO_PROD_LINES" -gt 0 ]; then
    TEST_RATIO=$((GO_TEST_LINES * 100 / (GO_PROD_LINES + GO_TEST_LINES)))
else
    TEST_RATIO=0
fi

# Top 15 largest Go files
TOP_GO_FILES=$(find . -name "*.go" ! -path "./.*" ! -path "./data/*" -exec wc -l {} + 2>/dev/null | sort -rn | head -16 | tail -15)

# Direct dependencies list
DIRECT_DEPS_LIST=$(awk '/^require \(/,/^\)/' go.mod | grep -v "^require" | grep -v "^)" | grep -v "// indirect" | sed 's/^[[:space:]]*//')

# Current date
CURRENT_DATE=$(date "+%B %d, %Y")

# --- Generate Report ---

cat > "$OUTPUT_FILE" << EOF
# Codebase Statistics Report

**Project:** \`go-web-nats\`  
**Go Version:** ${GO_VERSION}  
**Generated:** ${CURRENT_DATE}

---

## Summary

| Metric | Value |
| ------ | ----- |
| **Total Lines of Code** | ~${TOTAL_LINES} |
| **Total Source Files** | ${TOTAL_FILES} |
| **Primary Language** | Go |
| **Architecture** | HTTP API + Embedded Web App + NATS Backend |

---

## Lines of Code by Type

| File Type | Lines | Files | Description |
| --------- | ----- | ----- | ----------- |
| **Go (Production)** | ${GO_PROD_LINES} | ${GO_PROD_FILES} | Backend application code |
| **Go (Tests)** | ${GO_TEST_LINES} | ${GO_TEST_FILES} | Unit and integration tests |
| **JavaScript** | ${JS_LINES} | ${JS_FILES} | Frontend web application |
| **HTML** | ${HTML_LINES} | ${HTML_FILES} | Web UI template |
| **CSS** | ${CSS_LINES} | ${CSS_FILES} | Styling |
| **Markdown** | ${MD_LINES} | ${MD_FILES} | Documentation |
| **YAML** | ${YAML_LINES} | ${YAML_FILES} | Configuration |
| **JSON** | ${JSON_LINES} | ${JSON_FILES} | Schema definitions |

---

## Go Code Analysis

| Metric | Count |
| ------ | ----- |
| **Functions/Methods** | ${GO_FUNCTIONS} |
| **Structs** | ${GO_STRUCTS} |
| **Interfaces** | ${GO_INTERFACES} |
| **Test Functions** | ${GO_TEST_FUNCS} |
| **Test Coverage Ratio** | ${TEST_RATIO}% of Go code is tests |

### Top 15 Largest Go Files

| File | Lines | Category |
| ---- | ----- | -------- |
EOF

# Parse and add top Go files
echo "$TOP_GO_FILES" | while read -r line; do
    lines=$(echo "$line" | awk '{print $1}')
    file=$(echo "$line" | awk '{print $2}' | sed 's|^\./||')
    if [ -n "$file" ] && [ "$file" != "total" ]; then
        # Determine category
        category="Core"
        if [[ "$file" == *"_test.go" ]]; then
            category="Test"
        elif [[ "$file" == api_* ]]; then
            category="API"
        elif [[ "$file" == workers_* ]]; then
            category="Workers"
        elif [[ "$file" == store* ]]; then
            category="Storage"
        elif [[ "$file" == *events* ]]; then
            category="Events"
        elif [[ "$file" == config_* ]]; then
            category="Config"
        fi
        echo "| \`${file}\` | ${lines} | ${category} |" >> "$OUTPUT_FILE"
    fi
done

cat >> "$OUTPUT_FILE" << EOF

---

## Dependencies

| Type | Count |
| ---- | ----- |
| **Direct Dependencies** | ${DIRECT_DEPS} |
| **Indirect Dependencies** | ${INDIRECT_DEPS} |
| **Total** | ${TOTAL_DEPS} |

### Key Direct Dependencies

| Package | Purpose |
| ------- | ------- |
EOF

# Add direct dependencies
echo "$DIRECT_DEPS_LIST" | while read -r dep; do
    pkg=$(echo "$dep" | awk '{print $1}')
    if [ -n "$pkg" ]; then
        # Determine purpose based on package name
        purpose="Utility"
        if [[ "$pkg" == *"nats"* ]]; then
            if [[ "$pkg" == *"server"* ]]; then
                purpose="Embedded NATS server"
            else
                purpose="NATS client"
            fi
        elif [[ "$pkg" == *"go-git"* ]]; then
            purpose="Git operations"
        elif [[ "$pkg" == *"buildkit"* ]]; then
            purpose="Container image building"
        elif [[ "$pkg" == *"filepath-securejoin"* ]]; then
            purpose="Secure path handling"
        elif [[ "$pkg" == *"kustomize/api"* ]]; then
            purpose="Kubernetes manifest rendering"
        elif [[ "$pkg" == *"kustomize/kyaml"* ]]; then
            purpose="YAML processing"
        fi
        echo "| \`${pkg}\` | ${purpose} |" >> "$OUTPUT_FILE"
    fi
done

cat >> "$OUTPUT_FILE" << EOF

---

## API Endpoints

| Endpoint | Description |
| -------- | ----------- |
| \`GET/POST /api/projects\` | Project management |
| \`GET/PUT/DELETE /api/projects/{id}\` | Project by ID |
| \`GET /api/ops/{id}\` | Operation details |
| \`GET /api/events/registration\` | SSE registration events |
| \`GET /api/events/deployment\` | SSE deployment events |
| \`GET /api/events/promotion\` | SSE promotion events |
| \`GET /api/events/promotion/preview\` | Promotion preview events |
| \`GET /api/events/release\` | Release events |
| \`POST /api/webhooks/source\` | Source repo webhooks |
| \`GET /api/system\` | System info |
| \`GET /api/healthz\` | Health check |

---

## NATS Messaging Subjects

| Subject | Purpose |
| ------- | ------- |
| \`paas.project.op.start\` | Project operation initiation |
| \`paas.project.op.registration.done\` | Registration completion |
| \`paas.project.op.bootstrap.done\` | Bootstrap completion |
| \`paas.project.op.build.done\` | Build completion |
| \`paas.project.op.deploy.done\` | Deploy completion |
| \`paas.project.process.deployment.start\` | Deployment process start |
| \`paas.project.process.deployment.done\` | Deployment process done |
| \`paas.project.process.promotion.start\` | Promotion process start |
| \`paas.project.process.promotion.done\` | Promotion process done |

### KV Buckets

| Bucket | Purpose |
| ------ | ------- |
| \`paas_projects\` | Project state storage |
| \`paas_ops\` | Operation & release storage |

---

## Project Structure

\`\`\`
├── cmd/server/          # Server entrypoint
├── web/                 # Frontend (JS/HTML/CSS)
│   ├── app.js
│   ├── app_core.js
│   ├── app_data_artifacts.js
│   ├── app_events.js
│   ├── app_flow.js
│   ├── app_render_projects_ops.js
│   ├── app_render_surfaces.js
│   ├── index.html
│   └── styles.css
├── cfg/                 # Configuration examples
├── docs/                # API contracts & documentation
│   └── project-schema/  # JSON schema definitions
├── scripts/             # Build/task scripts
└── *.go                 # Core application (root)
\`\`\`

---

## JavaScript Analysis

| Metric | Value |
| ------ | ----- |
| **Functions** | ~${JS_FUNCTIONS} |
| **Files** | ${JS_FILES} |
| **Architecture** | Single-page app with modular JS |

### Frontend Files

| File | Description |
| ---- | ----------- |
| \`app.js\` | Main application entry |
| \`app_core.js\` | Core utilities |
| \`app_flow.js\` | Flow/state management |
| \`app_events.js\` | Event handling |
| \`app_data_artifacts.js\` | Artifact data layer |
| \`app_render_projects_ops.js\` | Project/ops rendering |
| \`app_render_surfaces.js\` | Surface rendering |

---

## Build System

The project uses a Makefile with 20+ targets:

### Testing

- \`test\` - Run unit tests
- \`test-race\` - Run tests with race detector
- \`test-api\` - Run API-focused tests
- \`test-workers\` - Run worker-focused tests
- \`test-store\` - Run store/artifact-focused tests
- \`test-model\` - Run model/spec-focused tests
- \`cover\` - Run tests with coverage report

### Code Quality

- \`fmt\` - Format Go files with gofumpt
- \`fmt-check\` - Fail if Go formatting is not gofumpt-clean
- \`vet\` - Run go vet
- \`lint\` - Run golangci-lint
- \`lint-fix\` - Run golangci-lint with auto-fix

### Utilities

- \`tidy\` - Sync Go module dependencies
- \`tools\` - Verify local toolchain dependencies
- \`js-check\` - Syntax-check frontend JS
- \`agent-check\` - Validate agent context files
- \`task-list\` - List task IDs from TASKMAP.yaml
- \`task-show\` - Show files/tests for a task

---

## Git Statistics

| Metric | Value |
| ------ | ----- |
| **Total Commits** | ${GIT_COMMITS} |
| **Contributors** | ${GIT_CONTRIBUTORS} |
| **Commits in $(date +%Y)** | ${GIT_COMMITS_THIS_YEAR} |
| **Default Branch** | main |

---

## Code Distribution

EOF

# Calculate percentages for visualization
if [ "$TOTAL_LINES" -gt 0 ]; then
    GO_PROD_PCT=$((GO_PROD_LINES * 1000 / TOTAL_LINES))
    GO_TEST_PCT=$((GO_TEST_LINES * 1000 / TOTAL_LINES))
    JS_PCT=$((JS_LINES * 1000 / TOTAL_LINES))
    MD_PCT=$((MD_LINES * 1000 / TOTAL_LINES))
    CSS_PCT=$((CSS_LINES * 1000 / TOTAL_LINES))
    HTML_PCT=$((HTML_LINES * 1000 / TOTAL_LINES))
    CONFIG_PCT=$(((YAML_LINES + JSON_LINES) * 1000 / TOTAL_LINES))
    
    # Format percentages with one decimal, right-aligned to 5 chars (e.g. "43.8%")
    fmt_pct() {
        local pct=$1
        printf "%5s" "$((pct / 10)).$((pct % 10))%"
    }
    
    GO_PROD_PCT_FMT=$(fmt_pct $GO_PROD_PCT)
    GO_TEST_PCT_FMT=$(fmt_pct $GO_TEST_PCT)
    JS_PCT_FMT=$(fmt_pct $JS_PCT)
    MD_PCT_FMT=$(fmt_pct $MD_PCT)
    CSS_PCT_FMT=$(fmt_pct $CSS_PCT)
    HTML_PCT_FMT=$(fmt_pct $HTML_PCT)
    CONFIG_PCT_FMT=$(fmt_pct $CONFIG_PCT)
    
    # Generate bar chart (45 chars wide = 100%)
    gen_bar() {
        local pct=$1
        local filled=$((pct * 45 / 1000))
        local empty=$((45 - filled))
        local bar=""
        for ((i=0; i<filled; i++)); do bar+="█"; done
        for ((i=0; i<empty; i++)); do bar+="░"; done
        printf '%s' "$bar"
    }
    
    cat >> "$OUTPUT_FILE" << EOF
\`\`\`
Go Production:  ${GO_PROD_PCT_FMT}  $(gen_bar $GO_PROD_PCT)
Go Tests:       ${GO_TEST_PCT_FMT}  $(gen_bar $GO_TEST_PCT)
JavaScript:     ${JS_PCT_FMT}  $(gen_bar $JS_PCT)
Markdown:       ${MD_PCT_FMT}  $(gen_bar $MD_PCT)
CSS:            ${CSS_PCT_FMT}  $(gen_bar $CSS_PCT)
HTML:           ${HTML_PCT_FMT}  $(gen_bar $HTML_PCT)
Config:         ${CONFIG_PCT_FMT}  $(gen_bar $CONFIG_PCT)
\`\`\`
EOF
fi

echo "✓ Generated ${OUTPUT_FILE}"
