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
        KIND_CLUSTER_NAME = 'battlefield'
        KUBERNETES_NAMESPACE = 'battlefield'
        KUBERNETES_CONTEXT = 'kind-battlefield'
        PREVIOUS_IMAGE_TAG = ''
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
                        sh './scripts/lint-go.sh'
                    }
                }

                stage('Backend tests and coverage') {
                    steps {
                        sh 'GOCACHE="/tmp/autonomous-battlefield-go-cache-${BUILD_NUMBER}" ./scripts/test-go-coverage.sh'
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
                        '''
                    }
                }
            }
            post {
                always {
                    script {
                        def summaryFile = '.jenkins-coverage-check.md'
                        def summaryStatus = sh(
                            script: "./scripts/coverage-check-summary.sh > ${summaryFile}",
                            returnStatus: true
                        )
                        def summary = fileExists(summaryFile)
                            ? readFile(summaryFile).trim()
                            : 'Coverage reports could not be summarized. Open the Jenkins build for details.'
                        def conclusion = summaryStatus == 0 ? 'SUCCESS' : 'FAILURE'

                        publishChecks(
                            name: 'Coverage',
                            title: summaryStatus == 0
                                ? 'Backend and frontend coverage generated'
                                : 'One or more coverage reports are unavailable',
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
                        sh './scripts/lint-go.sh'
                    }
                }

                stage('Main backend coverage') {
                    steps {
                        sh 'GOCACHE="/tmp/autonomous-battlefield-go-cache-${BUILD_NUMBER}" ./scripts/test-go-coverage.sh'
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
            }
        }

        stage('SonarQube analysis') {
            when {
                branch 'main'
            }
            steps {
                script {
                    def scannerHome = tool 'SonarScanner'
                    withSonarQubeEnv('battlefield-sonarqube') {
                        sh "${scannerHome}/bin/sonar-scanner -Dsonar.projectVersion=${env.IMAGE_TAG}"
                    }
                }
            }
        }

        stage('SonarQube quality gate') {
            when {
                branch 'main'
            }
            steps {
                timeout(time: 10, unit: 'MINUTES') {
                    waitForQualityGate abortPipeline: true
                }
            }
        }

        stage('Build immutable images') {
            when {
                anyOf {
                    changeRequest target: 'main'
                    branch 'main'
                }
            }
            steps {
                sh './scripts/build-images.sh'
            }
        }

        stage('Scan immutable images') {
            when {
                anyOf {
                    changeRequest target: 'main'
                    branch 'main'
                }
            }
            steps {
                sh './scripts/scan-images.sh "${IMAGE_TAG}"'
            }
        }

        stage('Deploy local kind') {
            when {
                branch 'main'
            }
            steps {
                script {
                    def previousImage = sh(
                        script: '''
                            kubectl --context "${KUBERNETES_CONTEXT}" \
                                get deployment/battlefield-api \
                                --namespace "${KUBERNETES_NAMESPACE}" \
                                -o jsonpath='{.spec.template.spec.containers[?(@.name=="api")].image}' \
                                2>/dev/null || true
                        ''',
                        returnStdout: true
                    ).trim()
                    env.PREVIOUS_IMAGE_TAG = previousImage.contains(':') ? previousImage.substring(previousImage.lastIndexOf(':') + 1) : ''
                    echo "Previous deployment SHA: ${env.PREVIOUS_IMAGE_TAG ?: 'none'}"
                }
                withCredentials([
                    file(
                        credentialsId: 'battlefield-local-secret-env',
                        variable: 'BATTLEFIELD_SECRET_FILE'
                    )
                ]) {
                    sh 'ROLLBACK_ON_FAILURE=1 ./scripts/deploy-kind.sh "${IMAGE_TAG}"'
                }
            }
        }

        stage('Verify deployment') {
            when {
                branch 'main'
            }
            steps {
                sh './scripts/smoke-test-kind.sh'
            }
            post {
                failure {
                    script {
                        if (env.PREVIOUS_IMAGE_TAG ==~ /[0-9a-f]{40}/) {
                            withCredentials([
                                file(
                                    credentialsId: 'battlefield-local-secret-env',
                                    variable: 'BATTLEFIELD_SECRET_FILE'
                                )
                            ]) {
                                sh './scripts/rollback-kind.sh "${PREVIOUS_IMAGE_TAG}"'
                            }
                        } else {
                            echo 'No previous immutable SHA is available for automatic rollback.'
                        }
                    }
                }
            }
        }
    }

    post {
        success {
            script {
                if (env.CHANGE_ID) {
                    echo "CI verified pull request ${env.CHANGE_ID} at ${env.IMAGE_TAG}."
                } else if (env.BRANCH_NAME == 'main') {
                    echo "CD deployed and verified ${env.IMAGE_TAG} in Jenkins build ${env.BUILD_NUMBER}."
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
