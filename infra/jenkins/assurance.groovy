pipelineJob('battlefield-assurance') {
    description('battlefield-ci-managed-v1: trusted-main offline assurance; activate after reviewing credentials')
    disabled(true)
    parameters { choiceParam('MODE', ['nightly', 'load', 'failure-drills', 'provider-eval', 'aws-promotion']) }
    triggers { cron('H H(1-4) * * *') }
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
            scriptPath('Jenkinsfile.assurance')
            lightweight(false)
        }
    }
}
