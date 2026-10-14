pipelineJob('battlefield-assurance') {
    description('battlefield-ci-managed-v1: trusted-main offline assurance; activate after reviewing credentials')
    disabled(true)
    // Run on demand only: production is not kept running, so a schedule would fail.
    parameters { choiceParam('MODE', ['offline', 'load', 'failure-drills', 'provider-eval', 'aws-promotion']) }
    definition {
        cpsScm {
            scm {
                git {
                    remote {
                        url('https://github.com/twhAuto/autonomous_battlefield.git')
                        credentials('github-app-battlefield')
                    }
                    branch('*/main')
                }
            }
            scriptPath('infra/jenkins/Jenkinsfile.assurance')
            lightweight(false)
        }
    }
}
