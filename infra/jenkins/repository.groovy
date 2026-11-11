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
                        // Only same-repository PRs build: every build runs PR code on the
                        // shared production agent, so fork PRs are never discovered.
                        gitHubPullRequestDiscovery { strategyId(1) }
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
