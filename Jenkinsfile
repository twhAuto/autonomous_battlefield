// Jenkins CI/CD entry point: verify pull requests targeting dev/main, then build and
// deliver changed components to production on main. Other branches skip these stages.
// Scripts under scripts/ci, scripts/test, and scripts/cd implement the individual steps.
pipeline {
    // Owner-approved shared agent for this small, trusted project; no host isolation.
    agent { label 'battlefield-production' }

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
    }

    parameters {
        booleanParam(name: 'APPLY_CONFIGURATION', defaultValue: false,
            description: 'Reapply production .env and restart application pods without requiring a source change.')
    }

    stages {
        stage('Checkout') {
            when {
                anyOf {
                    changeRequest target: 'main'
                    changeRequest target: 'dev'
                    branch 'main'
                }
            }
            steps {
                deleteDir()
                checkout scm
                script {
                    // Mutable values must not be shadowed by Declarative environment bindings.
                    env.DEPLOY_COMPONENTS = 'all'
                    env.PREVIOUS_BACKEND_IMAGE_REF = ''
                    env.PREVIOUS_FRONTEND_IMAGE_REF = ''
                    env.BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST = ''
                    env.CI_WORK_ROOT = "${pwd(tmp: true)}/autonomous-battlefield"
                    env.GOCACHE = "${env.CI_WORK_ROOT}/cache/go-build"
                    env.GOTMPDIR = "${env.CI_WORK_ROOT}/tmp/go"
                    env.TMPDIR = "${env.CI_WORK_ROOT}/tmp/general"
                    sh 'mkdir -p "${GOCACHE}" "${GOTMPDIR}" "${TMPDIR}"'
                    env.IMAGE_TAG = sh(
                        script: 'git rev-parse HEAD',
                        returnStdout: true
                    ).trim()
                    currentBuild.displayName = "#${env.BUILD_NUMBER} ${env.IMAGE_TAG.take(12)}"
                }
            }
        }

        stage('PR verification') {
            when {
                anyOf {
                    changeRequest target: 'main'
                    changeRequest target: 'dev'
                }
            }
            stages {
                stage('PR continuous integration') {
                    parallel {
                        stage('Go lint') {
                            steps {
                                sh './scripts/ci/lint-go.sh'
                            }
                        }

                        stage('Backend tests and coverage') {
                            steps {
                                sh './scripts/test/with-test-postgres.sh ./scripts/test/test-backend.sh'
                            }
                        }

                        stage('Source security and policy') {
                            steps {
                                sh './scripts/ci/security-and-policy.sh'
                            }
                        }

                        stage('Fuzz smoke') {
                            steps {
                                sh 'FUZZ_SECONDS=5 ./scripts/test/fuzz-smoke.sh'
                            }
                        }

                        stage('Deterministic AI evaluation') {
                            steps {
                                sh './scripts/test/test-ai-evals.sh'
                            }
                        }

                        stage('Frontend tests and coverage') {
                            steps {
                                dir('frontend') {
                                    sh 'npm ci'
                                    sh 'npm test -- --coverage'
                                    sh 'npm run build'
                                    sh 'npx playwright install chromium'
                                    // Executors share the host: serve Playwright on a free port, retrying only a lost port race.
                                    sh '../scripts/test/run-e2e.sh'
                                    // Browser-test evidence must survive the run; an empty archive would hide it.
                                    sh 'test -s playwright-report/index.html'
                                }
                            }
                        }

                        stage('Deployment script checks') {
                            steps {
                                sh '''
                                    ./scripts/test/test-deployment.sh
                                    ./scripts/test/test-docker-context.sh
                                    ./scripts/test/test-frontend-image.sh
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

                // Community Build has no dev-branch analysis, so each PR gets a preview project.
                stage('PR SonarQube analysis and quality gate') {
                    steps {
                        script {
                            def scannerHome = tool 'SonarScanner'
                            withSonarQubeEnv('battlefield-sonarqube') {
                                withEnv(["SONAR_SCANNER_HOME=${scannerHome}"]) {
                                    sh './scripts/ci/run-sonarqube-pr-gate.sh "${CHANGE_ID}" "origin/${CHANGE_TARGET}"'
                                }
                            }
                        }
                    }
                }

                stage('Build PR images') {
                    steps {
                        sh './scripts/ci/build-images.sh'
                    }
                }

                stage('Scan PR images') {
                    steps {
                        sh './scripts/ci/scan-images.sh "${IMAGE_TAG}"'
                    }
                }
            }
        }

        stage('Main delivery') {
            when {
                branch 'main'
            }
            stages {
                stage('Main quality reports') {
                    parallel {
                        stage('Main Go lint') {
                            steps {
                                sh './scripts/ci/lint-go.sh'
                            }
                        }

                        stage('Main backend coverage') {
                            steps {
                                sh './scripts/test/with-test-postgres.sh ./scripts/test/test-backend.sh'
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
                                    sh 'npx playwright install chromium'
                                    // Executors share the host: serve Playwright on a free port, retrying only a lost port race.
                                    sh '../scripts/test/run-e2e.sh'
                                    // Browser-test evidence must survive the run; an empty archive would hide it.
                                    sh 'test -s playwright-report/index.html'
                                }
                            }
                        }

                        stage('Main deterministic AI evaluation') {
                            steps {
                                sh './scripts/test/test-ai-evals.sh'
                            }
                        }
                    }
                }

                // Analysis consumes the coverage reports generated by the preceding stage.
                stage('SonarQube analysis and quality gate') {
                    steps {
                        script {
                            def scannerHome = tool 'SonarScanner'
                            withSonarQubeEnv('battlefield-sonarqube') {
                                withEnv(["SONAR_SCANNER_HOME=${scannerHome}"]) {
                                    sh './scripts/ci/run-sonarqube-gate.sh "${IMAGE_TAG}"'
                                }
                            }
                        }
                    }
                }

                stage('Determine deployment scope') {
                    steps {
                        sh '''
                            ./scripts/test/test-deployment.sh
                            ./scripts/test/test-docker-context.sh
                            ./scripts/test/test-frontend-image.sh
                        '''
                        script {
                            // Compare with the last successful build so changes from failed builds are included.
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
                            if (params.APPLY_CONFIGURATION && env.DEPLOY_COMPONENTS != 'all') {
                                def selected = env.DEPLOY_COMPONENTS == 'none' ? [] : env.DEPLOY_COMPONENTS.tokenize(',')
                                env.DEPLOY_COMPONENTS = (selected + ['infrastructure', 'application']).unique().join(',')
                            }
                            echo "Deployment components: ${env.DEPLOY_COMPONENTS}"
                        }
                    }
                }

                stage('Publish immutable images') {
                    when {
                        expression {
                            def components = (env.DEPLOY_COMPONENTS ?: 'all').tokenize(',')
                            components.contains('all') ||
                                components.contains('backend') ||
                                components.contains('frontend')
                        }
                    }
                    steps {
                        withCredentials([
                            usernamePassword(
                                credentialsId: 'battlefield-ghcr',
                                usernameVariable: 'GHCR_USER',
                                passwordVariable: 'GHCR_TOKEN'
                            )
                        ]) {
                            sh './scripts/ci/with-ghcr-auth.sh ./scripts/ci/publish-images.sh'
                        }
                    }
                }

                stage('Scan published images') {
                    when {
                        expression {
                            def components = (env.DEPLOY_COMPONENTS ?: 'all').tokenize(',')
                            components.contains('all') ||
                                components.contains('backend') ||
                                components.contains('frontend')
                        }
                    }
                    steps {
                        withCredentials([
                            usernamePassword(
                                credentialsId: 'battlefield-ghcr',
                                usernameVariable: 'GHCR_USER',
                                passwordVariable: 'GHCR_TOKEN'
                            )
                        ]) {
                            sh './scripts/ci/with-ghcr-auth.sh ./scripts/ci/scan-published-images.sh'
                        }
                    }
                }

                stage('Attest, sign, and verify images') {
                    when {
                        expression {
                            def components = (env.DEPLOY_COMPONENTS ?: 'all').tokenize(',')
                            components.contains('all') ||
                                components.contains('backend') ||
                                components.contains('frontend')
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
                            sh './scripts/ci/with-ghcr-auth.sh ./scripts/ci/attest-and-sign-images.sh'
                        }
                    }
                }

                stage('Production rollout') {
                    options { lock(resource: 'battlefield-production-platform') }
                    when { expression { (env.DEPLOY_COMPONENTS ?: 'all') != 'none' } }
                    environment {
                        DEPLOY_ENV = 'production'
                        KUBECONFIG = credentials('battlefield-production-kubeconfig')
                    }
                    stages {
                        stage('Validate CI credentials') {
                            steps {
                                withCredentials([file(credentialsId: 'battlefield-production-access-service-token', variable: 'BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE')]) {
                                    sh '''#!/usr/bin/env bash
                                        set -Eeuo pipefail
                                        set +x
                                        chmod 600 "$BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE"
                                        . ./scripts/lib/deployment-environment.sh
                                        battlefield_env_preflight
                                        python3 scripts/lib/access_service.py --origin "$TARGET_URL" --application "$BATTLEFIELD_AUTH_SECRET_FILE"
                                    '''
                                }
                            }
                        }
                        stage('Deploy production') {
                            steps {
                                script {
                                    // Capture the running images before rollout for recovery if smoke verification fails.
                                    def previousBackendImage = sh(
                                        script: '''#!/usr/bin/env bash
                                            set -Eeuo pipefail
                                            . ./scripts/lib/deployment-environment.sh
                                            battlefield_env_preflight
                                            kubectl --context "${KUBERNETES_CONTEXT}" \
                                                get deployment/battlefield-api \
                                                --namespace "${KUBERNETES_NAMESPACE}" \
                                                -o jsonpath='{.spec.template.spec.containers[?(@.name=="api")].image}' \
                                                2>/dev/null || true
                                        ''',
                                        returnStdout: true
                                    ).trim()
                                    def previousFrontendImage = sh(
                                        script: '''#!/usr/bin/env bash
                                            set -Eeuo pipefail
                                            . ./scripts/lib/deployment-environment.sh
                                            battlefield_env_preflight
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
                                    env.BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST = sh(
                                        script: './scripts/cd/production-recovery.sh read "$PREVIOUS_BACKEND_IMAGE_REF" "$PREVIOUS_FRONTEND_IMAGE_REF"',
                                        returnStdout: true
                                    ).trim()
                                    if (!env.BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST) {
                                        echo 'No matching verified recovery record; automatic rollback remains disabled for this rollout.'
                                    }
                                    echo "Previous backend image: ${env.PREVIOUS_BACKEND_IMAGE_REF ?: 'none'}"
                                    echo "Previous frontend image: ${env.PREVIOUS_FRONTEND_IMAGE_REF ?: 'none'}"
                                    sh 'mkdir -p artifacts/deployment'
                                    writeFile(
                                        file: 'artifacts/deployment/rollback-images.env',
                                        text: "PREVIOUS_BACKEND_IMAGE_REF=${env.PREVIOUS_BACKEND_IMAGE_REF}\nPREVIOUS_FRONTEND_IMAGE_REF=${env.PREVIOUS_FRONTEND_IMAGE_REF}\n"
                                    )
                                }
                                withCredentials([
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
                                        if [ -f artifacts/build/images.env ]; then
                                            set -a
                                            . artifacts/build/images.env
                                            set +a
                                        fi
                                        IMAGE_SOURCE=registry ENFORCE_SUPPLY_CHAIN_POLICY=1 ROLLBACK_ON_FAILURE=1 ./scripts/ci/with-ghcr-auth.sh ./scripts/cd/deploy.sh "${IMAGE_TAG}"
                                    '''
                                }
                            }
                        }

                        stage('Verify deployment') {
                            steps {
                                withCredentials([
                                    file(
                                        credentialsId: 'battlefield-production-access-service-token',
                                        variable: 'BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE'
                                    )
                                ]) {
                                    sh '''
                                        set +x
                                        chmod 600 "$BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE"
                                        ./scripts/cd/smoke-test.sh
                                    '''
                                }
                            }
                            post {
                                failure {
                                    script {
                                        def components = (env.DEPLOY_COMPONENTS ?: 'all').tokenize(',')
                                        def applicationChanged = components.contains('all') ||
                                            components.contains('backend') ||
                                            components.contains('frontend') ||
                                            components.contains('application')

                                        // Automatic image rollback requires an application change and both previous digests.
                                        if (!applicationChanged) {
                                            echo 'The targeted deployment did not change application images; skipping image rollback.'
                                        } else if (env.PREVIOUS_BACKEND_IMAGE_REF ==~ /[a-z0-9][a-z0-9._:\/-]+@sha256:[0-9a-f]{64}/ &&
                                            env.PREVIOUS_FRONTEND_IMAGE_REF ==~ /[a-z0-9][a-z0-9._:\/-]+@sha256:[0-9a-f]{64}/) {
                                            withCredentials([
                                                usernamePassword(credentialsId: 'battlefield-ghcr', usernameVariable: 'GHCR_USER', passwordVariable: 'GHCR_TOKEN'),
                                                file(credentialsId: 'battlefield-cosign-public-key', variable: 'COSIGN_PUBLIC_KEY_FILE')
                                            ]) {
                                                sh '''
                                                    ./scripts/cd/rollback.sh \
                                                        "${PREVIOUS_BACKEND_IMAGE_REF}" \
                                                        "${PREVIOUS_FRONTEND_IMAGE_REF}"
                                                '''
                                            }
                                        } else {
                                            echo 'Previous backend and frontend digest references are not both available for automatic rollback.'
                                        }
                                    }
                                }
                            }
                        }

                        stage('Runtime assurance') {
                            parallel {
                                stage('Smoke load') {
                                    steps {
                                        withCredentials([file(credentialsId: 'battlefield-production-access-service-token', variable: 'BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE')]) {
                                            sh '''
                                                set +x
                                                chmod 600 "$BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE"
                                                K6_VUS=3 K6_DURATION=30s ./scripts/perf/run-k6.sh
                                            '''
                                        }
                                    }
                                }
                                stage('ZAP baseline') {
                                    steps {
                                        withCredentials([file(credentialsId: 'battlefield-production-access-service-token', variable: 'BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE')]) {
                                            sh 'set +x; chmod 600 "$BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE"; ./scripts/security/zap-baseline.sh'
                                        }
                                    }
                                }
                            }
                        }
                        stage('Record verified recovery') {
                            steps {
                                sh './scripts/cd/production-recovery.sh record "$IMAGE_TAG"'
                            }
                        }

                    }
                }
            }
        }
    }

    post {
        always {
            script {
                if (env.CI_WORK_ROOT) {
                    dir(env.CI_WORK_ROOT) {
                        deleteDir()
                    }
                }
            }
            archiveArtifacts(
                artifacts: 'artifacts/**/*,backend/coverage.out,backend/coverage-total.txt,frontend/coverage/**/*,frontend/test-results/**/*',
                allowEmptyArchive: true,
                fingerprint: true
            )
            // Archived separately: when the browser tests ran, their report must not go missing
            // unnoticed, so this archive is not allowed to come back empty.
            script {
                if (fileExists('frontend/playwright-report/index.html')) {
                    archiveArtifacts(
                        artifacts: 'frontend/playwright-report/**/*',
                        allowEmptyArchive: false,
                        fingerprint: true
                    )
                } else {
                    echo 'No Playwright report was produced; browser-test evidence is unavailable for this build.'
                }
            }
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
