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

        stage('Test') {
            parallel {
                stage('Backend tests') {
                    steps {
                        dir('backend') {
                            sh 'GOCACHE="/tmp/autonomous-battlefield-go-cache-${BUILD_NUMBER}" go test ./...'
                        }
                    }
                }

                stage('Frontend tests') {
                    steps {
                        dir('frontend') {
                            sh 'npm ci'
                            sh 'npm test'
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
        }

        stage('Build images') {
            steps {
                sh './scripts/build-images.sh'
            }
        }

        stage('Scan images') {
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
            echo "Verified ${env.IMAGE_TAG} in Jenkins build ${env.BUILD_NUMBER}."
        }
        failure {
            echo "Jenkins build ${env.BUILD_NUMBER} failed. Review the failed stage and rollback output."
        }
    }
}
