# Repository Branch Structure

**Date:** 2025-12-02
**Repository:** https://github.com/ipiton/AMP

---

## 🎯 **Branch Strategy**

We use a **clean main** strategy to keep the primary branch minimal and production-ready.

---

## 📋 **Branches**

### `main` (default branch) ✅

**Purpose:** Minimal, production-ready OSS release
**Status:** **CLEAN** - Maximum 16 files
**Size:** ~8.4 MB

**What's Included:**
```
├── go-app/                  # Core Go application
├── pkg/core/                # Core interfaces & domain models
├── examples/                # Extension examples (2)
├── docs/                    # Migration guides
├── Dockerfile               # Minimal Go build
├── README.md                # Project overview
├── LICENSE                  # Apache 2.0
├── CODE_OF_CONDUCT.md       # Community guidelines
├── SECURITY.md              # Security policy
├── CONTRIBUTING.md          # Contribution guidelines
├── CHANGELOG.md             # Release history
└── .gitignore               # Git ignore rules
```

**What's NOT Included:**
- ❌ CI/CD workflows (`.github/workflows/`)
- ❌ Issue templates (`.github/ISSUE_TEMPLATE/`)
- ❌ Dependabot config
- ❌ docker-compose.yml
- ❌ BYK LLM planning documents
- ❌ Community infrastructure reports
- ❌ .editorconfig, .gitattributes, etc.

**Philosophy:**
> "main should be so clean you can release it at any moment."

---

### `feature/community-infrastructure` 🚧

**Purpose:** Full community infrastructure for OSS project
**Status:** Complete, ready for PR
**Files Added:** 20+ professional community files

**What's Added:**

#### 1️⃣ **Issue Management (3 files)**
```
.github/ISSUE_TEMPLATE/
├── bug_report.yml           # Structured bug reports
├── feature_request.yml      # Feature requests
├── byk_llm_feature.md       # BYK LLM tracking
└── config.yml               # Contact links
```

#### 2️⃣ **CI/CD Workflows (6 files)**
```
.github/workflows/
├── ci.yml                   # Lint, test, build, security scan
├── release.yml              # Automated releases
├── stale.yml                # Stale issue cleanup
├── labeler.yml              # Auto-label PRs
├── dependabot.yml           # Dependency updates
└── labeler.yml (config)
```

#### 3️⃣ **Docker & Compose**
```
├── Dockerfile               # Enhanced multi-stage build
├── .dockerignore            # Optimized context
└── docker-compose.yml       # Full stack (amp + postgres + redis + prometheus)
```

#### 4️⃣ **Project Standards**
```
├── ROADMAP.md               # v1.x and v2.x vision
├── AUTHORS.md               # Contributor recognition
├── .editorconfig            # Code formatting
├── .gitattributes           # Git settings
└── .github/CODEOWNERS       # Code review assignments
```

#### 5️⃣ **BYK LLM Planning (3 files)**
```
├── BYK_LLM_PLAN.md                 # Implementation plan (257 lines)
├── BYK_LLM_CLARIFICATION.md        # Explanation (271 lines)
└── .github/ISSUE_TEMPLATE/byk_llm_feature.md  # Tracking template
```

#### 6️⃣ **Summary Documents**
```
├── OSS_COMMUNITY_COMPLETE.md       # Community setup report
├── FINAL_OSS_RELEASE_SUMMARY.md    # Final summary
└── examples/prometheus/prometheus.yml  # Prometheus config
```

**Total:** ~5,000 lines of community infrastructure

---

## 🔀 **Workflow**

### For Users (Download/Clone):
```bash
# Get clean minimal version
git clone https://github.com/ipiton/AMP.git
cd AMP

# Branch: main (default)
# Result: Minimal production-ready code
```

### For Contributors:
```bash
# Get full development version
git clone https://github.com/ipiton/AMP.git
cd AMP

# Checkout feature branch
git checkout feature/community-infrastructure

# Result: Full CI/CD, Issue templates, etc.
```

### For Maintainers:
```bash
# When ready to merge community infrastructure:
git checkout main
git merge feature/community-infrastructure

# Or keep separate:
# - main: Minimal for users
# - feature/community-infrastructure: Full for contributors
```

---

## 📊 **Comparison**

| Item | main (Clean) | feature/community-infrastructure |
|------|--------------|----------------------------------|
| **Files** | 16 | 40+ |
| **Size** | ~8.4 MB | ~9 MB |
| **Purpose** | User-facing release | Contributor infrastructure |
| **CI/CD** | ❌ None | ✅ 4 workflows |
| **Issue Templates** | ❌ None | ✅ 3 templates |
| **docker-compose** | ❌ No | ✅ Full stack |
| **Docs** | ✅ Migration guides | ✅ + BYK LLM plans + summaries |
| **Simplicity** | ✅✅✅ Maximum | ⚙️ Full featured |

---

## 🎯 **Why This Structure?**

### Benefits of Clean Main:
1. **First Impressions** - Users see minimal, focused codebase
2. **Fast Clone** - Less files = faster git clone
3. **Less Overwhelming** - New contributors aren't buried in infrastructure
4. **Clear Scope** - Separation of product code vs project infrastructure
5. **Release Ready** - main is always deployable

### When to Merge feature/community-infrastructure:
- ✅ **After first community contributions** - When issues/PRs start coming
- ✅ **When ready for automation** - CI/CD saves maintainer time
- ✅ **For serious project** - Shows professionalism
- ❌ **Too early** - Can overwhelm early adopters

---

## 📅 **Timeline**

| Date | Branch | Action |
|------|--------|--------|
| 2025-12-02 | `main` | Initial OSS release (495495a) |
| 2025-12-02 | `main` | Clean paid features (1f04ff6) |
| 2025-12-02 | `main` | Add CHANGELOG + Go Dockerfile (9170c12) |
| 2025-12-02 | `feature/community-infrastructure` | Created with full infrastructure |
| 2025-12-02 | `main` | Reset to clean state (force push) |
| 2025-12-02 | `main` | Tag v1.0.0-preview (clean) |

---

## 🚀 **Current Status**

### main branch:
```
Repository: https://github.com/ipiton/AMP
Branch: main
Commits: 4
Tag: v1.0.0-preview (9170c12)
Status: ✅ CLEAN & PRODUCTION-READY
```

### feature/community-infrastructure branch:
```
Branch: feature/community-infrastructure
Commits: 11 (all infrastructure)
Status: ✅ COMPLETE, READY FOR PR
PR: https://github.com/ipiton/AMP/pull/new/feature/community-infrastructure
```

---

## 📝 **Recommendations**

### For v1.0.0 Release:
- ✅ Use **main** branch (clean)
- ✅ Tag: v1.0.0-preview
- ✅ Release notes: Focus on features, not infrastructure

### For v1.1.0 Release (with BYK LLM):
- ✅ Merge `feature/community-infrastructure` → `main`
- ✅ Enable CI/CD
- ✅ Start accepting community PRs
- ✅ Implement BYK LLM (7-9h)

---

## 🔗 **Links**

- **Main Branch:** https://github.com/ipiton/AMP/tree/main
- **Feature Branch:** https://github.com/ipiton/AMP/tree/feature/community-infrastructure
- **Create PR:** https://github.com/ipiton/AMP/pull/new/feature/community-infrastructure
- **Release:** https://github.com/ipiton/AMP/releases/tag/v1.0.0-preview

---

**Created:** 2025-12-02
**Strategy:** Clean main + feature branches
**Philosophy:** Simplicity first, infrastructure when needed
**Status:** ✅ IMPLEMENTED
