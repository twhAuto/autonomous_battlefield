multibranchPipelineJob('autonomous-battlefield') {
    description('battlefield-ci-managed-v1: repository verification and main delivery')
    configure { root ->
        root.children().removeAll { it instanceof Node && it.name() == 'disabled' }
        root.appendNode('disabled', 'true')
    }
    branchSources {
        branchSource {
            source {
                github {
                    id('1')
                    credentialsId('github-app-battlefield')
                    repoOwner('twhAuto')
                    repository('autonomous_battlefield')
                    repositoryUrl('https://github.com/twhAuto/autonomous_battlefield.git')
                    configuredByUrl(false)
                    traits {
                        gitHubBranchDiscovery { strategyId(1) }
                        gitHubPullRequestDiscovery { strategyId(1) }
                        gitHubForkDiscovery {
                            strategyId(2)
                            trust { gitHubTrustPermissions() }
                        }
                        headRegexFilter { regex('^(main|PR-[0-9]+)$') }
                    }
                }
            }
        }
    }
    factory { workflowBranchProjectFactory { scriptPath('Jenkinsfile') } }
    orphanedItemStrategy { discardOldItems { daysToKeep(30); numToKeep(20) } }
    triggers { periodicFolderTrigger { interval('2m') } }
}
