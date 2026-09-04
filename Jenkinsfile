pipeline {
    agent any

    options {
        buildDiscarder(logRotator(numToKeepStr: '20'))
        disableConcurrentBuilds()
        skipDefaultCheckout(true)
        timestamps()
        timeout(time: 45, unit: 'MINUTES')
    }

    environment {
        BACKEND_IMAGE = 'autonomous-battlefield-backend'
        FRONTEND_IMAGE = 'autonomous-battlefield-frontend'
        GO_COVERAGE_MIN = '80'
        KIND_CLUSTER_NAME = 'battlefield'
        KUBERNETES_NAMESPACE = 'battlefield'
        KUBERNETES_CONTEXT = 'kind-battlefield'
        DEPLOY_COMPONENTS = 'all'
        PREVIOUS_BACKEND_IMAGE_REF = ''
        PREVIOUS_FRONTEND_IMAGE_REF = ''
        TRIVY_IMAGE = 'ghcr.io/aquasecurity/trivy:0.74.0@sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969'
    }

    stages {
        stage('Checkout') {
            when {
                anyOf {
                    changeRequest target: 'main'
                    branch 'main'
                }
            }
            steps {
                deleteDir()
                checkout scm
                script {
                    env.IMAGE_TAG = sh(
                        script: 'git rev-parse HEAD',
                        returnStdout: true
                    ).trim()
                    currentBuild.displayName = "#${env.BUILD_NUMBER} ${env.IMAGE_TAG.take(12)}"
                }
            }
        }

        stage('PR continuous integration') {
            when {
                changeRequest target: 'main'
            }
            parallel {
                stage('Go lint') {
                    steps {
                        sh './scripts/ci/lint-go.sh'
                    }
                }

                stage('Backend tests and coverage') {
                    steps {
                        sh 'GOCACHE="/tmp/autonomous-battlefield-go-cache-${BUILD_NUMBER}" ./scripts/ci/with-test-postgres.sh ./scripts/ci/test-backend.sh'
                    }
                }

                stage('Source security and policy') {
                    steps {
                        sh './scripts/ci/security-and-policy.sh'
                    }
                }

                stage('Fuzz smoke') {
                    steps {
                        sh 'FUZZ_SECONDS=5 ./scripts/ci/fuzz-smoke.sh'
                    }
                }

                stage('Deterministic AI evaluation') {
                    steps {
                        sh './scripts/ci/test-ai-evals.sh'
                    }
                }

                stage('Frontend tests and coverage') {
                    steps {
                        dir('frontend') {
                            sh 'npm ci'
                            sh 'npm test -- --coverage'
                            sh 'npm run build'
                        }
                    }
                }

                stage('Kubernetes render') {
                    steps {
                        sh '''
                            kubectl kustomize deploy/base/infrastructure >/dev/null
                            kubectl kustomize deploy/overlays/local/migration >/dev/null
                            kubectl kustomize deploy/overlays/local/application >/dev/null
                            kubectl kustomize deploy/overlays/local/observability >/dev/null
                            kubectl kustomize deploy/overlays/local/logging-collector >/dev/null
                            kubectl kustomize deploy/overlays/local/gateway >/dev/null
                            ./scripts/ci/test-deployment-components.sh
                            ./scripts/ci/test-kind-deployment.sh
                        '''
                    }
                }
            }
            post {
                always {
                    script {
                        def summaryFile = '.jenkins-coverage-check.md'
                        def summaryStatus = sh(
                            script: "./scripts/ci/coverage-check-summary.sh > ${summaryFile}",
                            returnStatus: true
                        )
                        def summary = fileExists(summaryFile)
                            ? readFile(summaryFile).trim()
                            : 'Coverage reports could not be summarized. Open the Jenkins build for details.'
                        def conclusion = summaryStatus == 0 ? 'SUCCESS' : 'FAILURE'

                        publishChecks(
                            name: 'Coverage',
                            title: summaryStatus == 0
                                ? 'Backend and frontend coverage requirements met'
                                : 'Coverage requirements not met or reports unavailable',
                            summary: summary,
                            detailsURL: "${env.BUILD_URL}console",
                            status: 'COMPLETED',
                            conclusion: conclusion
                        )
                    }
                }
            }
        }

        stage('Main quality reports') {
            when {
                branch 'main'
            }
            parallel {
                stage('Main Go lint') {
                    steps {
                        sh './scripts/ci/lint-go.sh'
                    }
                }

                stage('Main backend coverage') {
                    steps {
                        sh 'GOCACHE="/tmp/autonomous-battlefield-go-cache-${BUILD_NUMBER}" ./scripts/ci/with-test-postgres.sh ./scripts/ci/test-backend.sh'
                    }
                }

                stage('Main source security and policy') {
                    steps {
                        sh './scripts/ci/security-and-policy.sh'
                    }
                }

                stage('Main frontend coverage') {
                    steps {
                        dir('frontend') {
                            sh 'npm ci'
                            sh 'npm test -- --coverage'
                            sh 'npm run build'
                        }
                    }
                }

                stage('Main deterministic AI evaluation') {
                    steps {
                        sh './scripts/ci/test-ai-evals.sh'
                    }
                }
            }
        }

        stage('SonarQube analysis and quality gate') {
            when {
                branch 'main'
            }
            steps {
                script {
                    def scannerHome = tool 'SonarScanner'
                    withSonarQubeEnv('battlefield-sonarqube') {
                        sh """
                            "${scannerHome}/bin/sonar-scanner" \
                                -Dsonar.projectVersion="${env.IMAGE_TAG}" \
                                -Dsonar.qualitygate.wait=true \
                                -Dsonar.qualitygate.timeout=300
                        """
                    }
                }
            }
        }

        stage('Determine deployment scope') {
            when {
                branch 'main'
            }
            steps {
                sh '''
                    ./scripts/ci/test-deployment-components.sh
                    ./scripts/ci/test-kind-deployment.sh
                '''
                script {
                    def previousSuccessfulCommit = env.GIT_PREVIOUS_SUCCESSFUL_COMMIT ?: ''
                    withEnv(["DEPLOY_BASE_REVISION=${previousSuccessfulCommit}"]) {
                        env.DEPLOY_COMPONENTS = sh(
                            script: '''
                                ./scripts/cd/detect-deployment-components.sh \
                                    "${DEPLOY_BASE_REVISION}" \
                                    "${IMAGE_TAG}"
                            ''',
                            returnStdout: true
                        ).trim()
                    }
                    echo "Deployment components: ${env.DEPLOY_COMPONENTS}"
                }
            }
        }

        stage('Build immutable images') {
            when {
                anyOf {
                    changeRequest target: 'main'
                    allOf {
                        branch 'main'
                        expression {
                            def components = (env.DEPLOY_COMPONENTS ?: 'all').tokenize(',')
                            components.contains('all') ||
                                components.contains('backend') ||
                                components.contains('frontend')
                        }
                    }
                }
            }
            steps {
                script {
                    if (env.BRANCH_NAME == 'main') {
                        withCredentials([
                            usernamePassword(
                                credentialsId: 'battlefield-ghcr',
                                usernameVariable: 'GHCR_USER',
                                passwordVariable: 'GHCR_TOKEN'
                            )
                        ]) {
                            sh '''
                                docker_config="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-docker-config.XXXXXX")"
                                chmod 700 "${docker_config}"
                                cleanup_docker_config() {
                                    docker logout ghcr.io >/dev/null 2>&1 || true
                                    rm -rf -- "${docker_config}"
                                }
                                trap cleanup_docker_config EXIT
                                export BUILDX_CONFIG="${BUILDX_CONFIG:-${HOME}/.docker/buildx}"
                                export DOCKER_CONFIG="${docker_config}"
                                printf '%s' "${GHCR_TOKEN}" | docker login ghcr.io --username "${GHCR_USER}" --password-stdin
                                ./scripts/ci/publish-images.sh
                            '''
                        }
                    } else {
                        sh './scripts/ci/build-images.sh'
                    }
                }
            }
        }

        stage('Scan immutable images') {
            when {
                anyOf {
                    changeRequest target: 'main'
                    allOf {
                        branch 'main'
                        expression {
                            def components = (env.DEPLOY_COMPONENTS ?: 'all').tokenize(',')
                            components.contains('all') ||
                                components.contains('backend') ||
                                components.contains('frontend')
                        }
                    }
                }
            }
            steps {
                script {
                    if (env.BRANCH_NAME == 'main') {
                        withCredentials([
                            usernamePassword(
                                credentialsId: 'battlefield-ghcr',
                                usernameVariable: 'GHCR_USER',
                                passwordVariable: 'GHCR_TOKEN'
                            )
                        ]) {
                            sh './scripts/ci/scan-published-images.sh'
                        }
                    } else {
                        sh './scripts/ci/scan-images.sh "${IMAGE_TAG}"'
                    }
                }
            }
        }

        stage('Attest, sign, and verify images') {
            when {
                allOf {
                    branch 'main'
                    expression {
                        def components = (env.DEPLOY_COMPONENTS ?: 'all').tokenize(',')
                        components.contains('all') ||
                            components.contains('backend') ||
                            components.contains('frontend')
                    }
                }
            }
            steps {
                withCredentials([
                    usernamePassword(
                        credentialsId: 'battlefield-ghcr',
                        usernameVariable: 'GHCR_USER',
                        passwordVariable: 'GHCR_TOKEN'
                    ),
                    file(
                        credentialsId: 'battlefield-cosign-key',
                        variable: 'COSIGN_KEY_FILE'
                    ),
                    string(
                        credentialsId: 'battlefield-cosign-password',
                        variable: 'COSIGN_PASSWORD'
                    ),
                    file(
                        credentialsId: 'battlefield-cosign-public-key',
                        variable: 'COSIGN_PUBLIC_KEY_FILE'
                    )
                ]) {
                    sh '''
                        docker_config="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-docker-config.XXXXXX")"
                        chmod 700 "${docker_config}"
                        cleanup_docker_config() {
                            docker logout ghcr.io >/dev/null 2>&1 || true
                            rm -rf -- "${docker_config}"
                        }
                        trap cleanup_docker_config EXIT
                        export BUILDX_CONFIG="${BUILDX_CONFIG:-${HOME}/.docker/buildx}"
                        export DOCKER_CONFIG="${docker_config}"
                        printf '%s' "${GHCR_TOKEN}" | docker login ghcr.io --username "${GHCR_USER}" --password-stdin
                        ./scripts/ci/attest-and-sign-images.sh
                    '''
                }
            }
        }

        stage('Deploy local kind') {
            when {
                allOf {
                    branch 'main'
                    expression {
                        (env.DEPLOY_COMPONENTS ?: 'all') != 'none'
                    }
                }
            }
            steps {
                script {
                    def previousBackendImage = sh(
                        script: '''
                            kubectl --context "${KUBERNETES_CONTEXT}" \
                                get deployment/battlefield-api \
                                --namespace "${KUBERNETES_NAMESPACE}" \
                                -o jsonpath='{.spec.template.spec.containers[?(@.name=="api")].image}' \
                                2>/dev/null || true
                        ''',
                        returnStdout: true
                    ).trim()
                    def previousFrontendImage = sh(
                        script: '''
                            kubectl --context "${KUBERNETES_CONTEXT}" \
                                get deployment/battlefield-frontend \
                                --namespace "${KUBERNETES_NAMESPACE}" \
                                -o jsonpath='{.spec.template.spec.containers[?(@.name=="frontend")].image}' \
                                2>/dev/null || true
                        ''',
                        returnStdout: true
                    ).trim()
                    env.PREVIOUS_BACKEND_IMAGE_REF = previousBackendImage
                    env.PREVIOUS_FRONTEND_IMAGE_REF = previousFrontendImage
                    echo "Previous backend image: ${env.PREVIOUS_BACKEND_IMAGE_REF ?: 'none'}"
                    echo "Previous frontend image: ${env.PREVIOUS_FRONTEND_IMAGE_REF ?: 'none'}"
                    sh 'mkdir -p artifacts/deployment'
                    writeFile(
                        file: 'artifacts/deployment/rollback-images.env',
                        text: "PREVIOUS_BACKEND_IMAGE_REF=${env.PREVIOUS_BACKEND_IMAGE_REF}\nPREVIOUS_FRONTEND_IMAGE_REF=${env.PREVIOUS_FRONTEND_IMAGE_REF}\n"
                    )
                }
                withCredentials([
                    file(
                        credentialsId: 'battlefield-local-secret-env',
                        variable: 'BATTLEFIELD_SECRET_FILE'
                    ),
                    usernamePassword(
                        credentialsId: 'battlefield-ghcr',
                        usernameVariable: 'GHCR_USER',
                        passwordVariable: 'GHCR_TOKEN'
                    ),
                    file(
                        credentialsId: 'battlefield-cosign-public-key',
                        variable: 'COSIGN_PUBLIC_KEY_FILE'
                    )
                ]) {
                    sh '''
                        docker_config="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-docker-config.XXXXXX")"
                        chmod 700 "${docker_config}"
                        cleanup_docker_config() {
                            docker logout ghcr.io >/dev/null 2>&1 || true
                            rm -rf -- "${docker_config}"
                        }
                        trap cleanup_docker_config EXIT
                        export BUILDX_CONFIG="${BUILDX_CONFIG:-${HOME}/.docker/buildx}"
                        export DOCKER_CONFIG="${docker_config}"
                        printf '%s' "${GHCR_TOKEN}" | docker login ghcr.io --username "${GHCR_USER}" --password-stdin
                        if [ -f artifacts/build/images.env ]; then
                            set -a
                            . artifacts/build/images.env
                            set +a
                        fi
                        IMAGE_SOURCE=registry ENFORCE_SUPPLY_CHAIN_POLICY=1 ROLLBACK_ON_FAILURE=1 ./scripts/cd/deploy-kind.sh "${IMAGE_TAG}"
                    '''
                }
            }
        }

        stage('Verify deployment') {
            when {
                allOf {
                    branch 'main'
                    expression {
                        (env.DEPLOY_COMPONENTS ?: 'all') != 'none'
                    }
                }
            }
            steps {
                sh './scripts/cd/smoke-test-kind.sh'
            }
            post {
                failure {
                    script {
                        def components = (env.DEPLOY_COMPONENTS ?: 'all').tokenize(',')
                        def applicationChanged = components.contains('all') ||
                            components.contains('backend') ||
                            components.contains('frontend') ||
                            components.contains('application')

                        if (!applicationChanged) {
                            echo 'The targeted deployment did not change application images; skipping image rollback.'
                        } else if (env.PREVIOUS_BACKEND_IMAGE_REF ==~ /[a-z0-9][a-z0-9._:\/-]+@sha256:[0-9a-f]{64}/ &&
                            env.PREVIOUS_FRONTEND_IMAGE_REF ==~ /[a-z0-9][a-z0-9._:\/-]+@sha256:[0-9a-f]{64}/) {
                            sh '''
                                ./scripts/cd/rollback-kind.sh \
                                    "${PREVIOUS_BACKEND_IMAGE_REF}" \
                                    "${PREVIOUS_FRONTEND_IMAGE_REF}"
                            '''
                        } else {
                            echo 'Previous backend and frontend digest references are not both available for automatic rollback.'
                        }
                    }
                }
            }
        }

        stage('Runtime assurance') {
            when {
                allOf {
                    branch 'main'
                    expression { (env.DEPLOY_COMPONENTS ?: 'all') != 'none' }
                }
            }
            parallel {
                stage('Smoke load') {
                    steps {
                        sh 'K6_VUS=3 K6_DURATION=30s ./scripts/perf/run-k6.sh'
                    }
                }
                stage('ZAP baseline') {
                    steps {
                        sh './scripts/security/zap-baseline.sh'
                    }
                }
            }
        }
    }

    post {
        always {
            archiveArtifacts(
                artifacts: 'artifacts/**/*,backend/coverage.out,backend/coverage-total.txt,frontend/coverage/**/*',
                allowEmptyArchive: true,
                fingerprint: true
            )
        }
        success {
            script {
                if (env.CHANGE_ID) {
                    echo "CI verified pull request ${env.CHANGE_ID} at ${env.IMAGE_TAG}."
                } else if (env.BRANCH_NAME == 'main') {
                    if (env.DEPLOY_COMPONENTS == 'none') {
                        echo "CI verified ${env.IMAGE_TAG}; no deployable components changed."
                    } else {
                        echo "CD deployed ${env.DEPLOY_COMPONENTS} and verified ${env.IMAGE_TAG} in Jenkins build ${env.BUILD_NUMBER}."
                    }
                } else {
                    echo "No CI/CD action is configured for a direct ${env.BRANCH_NAME} branch push."
                }
            }
        }
        failure {
            echo "Jenkins build ${env.BUILD_NUMBER} failed. Review the failed stage and rollback output."
        }
    }
}
