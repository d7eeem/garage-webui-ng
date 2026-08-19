// Jenkins port of .github/workflows/ci.yml + docker-publish.yml.
// release.yml (signed binaries on v* tags) is NOT ported yet.
// Toolchain versions (Node 20, Go 1.25.13) are baked into the agent image;
// pnpm resolves from package.json's "packageManager" via corepack.
// Publish runs only on main: multi-arch (amd64+arm64) buildx push to GHCR
// using the `github-pat` credential; binfmt is installed on the host on demand.
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

    stage('Build & push multi-arch image') {
      when { branch 'main' }
      environment {
        IMAGE = 'ghcr.io/d7eeem/garage-webui-ng'
      }
      steps {
        withCredentials([usernamePassword(credentialsId: 'github-pat',
            usernameVariable: 'REG_USER', passwordVariable: 'REG_TOKEN')]) {
          sh 'echo "$REG_TOKEN" | docker login ghcr.io -u "$REG_USER" --password-stdin'
        }
        sh '''
          set -eu
          # QEMU user-mode emulation for the arm64 half of the build; idempotent.
          docker run --privileged --rm tonistiigi/binfmt --install arm64
          docker buildx create --name jenkins-multiarch --driver docker-container --use 2>/dev/null \
            || docker buildx use jenkins-multiarch
          SHORT=$(git rev-parse --short HEAD)
          docker buildx build \
            --platform linux/amd64,linux/arm64 \
            -t "$IMAGE:main" \
            -t "$IMAGE:latest" \
            -t "$IMAGE:sha-$SHORT" \
            --build-arg VERSION=main \
            --label org.opencontainers.image.title="Garage WebUI-NG" \
            --label org.opencontainers.image.description="Modern admin dashboard for Garage S3-compatible object storage." \
            --label org.opencontainers.image.licenses=MIT \
            --label org.opencontainers.image.vendor=garage-webui-ng \
            --cache-from type=registry,ref="$IMAGE:buildcache" \
            --cache-to type=registry,ref="$IMAGE:buildcache",mode=max \
            --push .
        '''
      }
      post {
        always {
          sh 'docker logout ghcr.io || true'
        }
      }
    }
  }
}
