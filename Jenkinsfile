// Jenkins port of .github/workflows/ci.yml — runs on the docker-host agent.
// Docker publish and release stay on GitHub Actions (self-hosted runner) for now.
// Toolchain versions (Node 20, Go 1.25.13) are baked into the agent image;
// pnpm resolves from package.json's "packageManager" via corepack.
pipeline {
  agent { label 'docker' }

  options {
    disableConcurrentBuilds(abortPrevious: true)
  }

  stages {
    stage('Frontend: install') {
      steps {
        sh 'pnpm install --frozen-lockfile'
      }
    }

    // Non-blocking: a pre-existing lint backlog (mostly @typescript-eslint/
    // no-explicit-any) is tracked separately. Lint still runs so violations
    // stay visible; drop catchError once `pnpm run lint` exits 0.
    stage('Frontend: lint (non-blocking)') {
      steps {
        catchError(buildResult: 'SUCCESS', stageResult: 'UNSTABLE') {
          sh 'pnpm run lint'
        }
      }
    }

    stage('Frontend: typecheck · test · build') {
      steps {
        sh 'pnpm run typecheck'
        sh 'pnpm run test'
        sh 'pnpm run build'
      }
    }

    stage('Backend: build · vet · fmt · test') {
      steps {
        dir('backend') {
          sh 'go build ./...'
          sh 'go vet ./...'
          sh 'test -z "$(gofmt -l .)"'
          sh 'go test -race ./...'
        }
      }
    }

    // govulncheck reports stdlib advisories against the toolchain it runs
    // under — the agent image pins the same Go patch the backend builds with.
    stage('Security: govulncheck') {
      steps {
        dir('backend') {
          sh '''
            go install golang.org/x/vuln/cmd/govulncheck@latest
            "$(go env GOPATH)/bin/govulncheck" ./...
          '''
        }
      }
    }

    stage('Security: pnpm audit (advisory)') {
      steps {
        catchError(buildResult: 'SUCCESS', stageResult: 'UNSTABLE') {
          sh 'pnpm audit --prod'
        }
      }
    }
  }
}
