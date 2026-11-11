// Scoped reconciliation: existing identities and unrelated objects are retained.
import groovy.json.JsonSlurper
import jenkins.model.Jenkins
import hudson.model.User
import hudson.model.Node
import hudson.model.Computer
import hudson.slaves.DumbSlave
import hudson.slaves.JNLPLauncher
import hudson.security.HudsonPrivateSecurityRealm
import jenkins.security.ApiTokenProperty
import com.cloudbees.plugins.credentials.SystemCredentialsProvider
import com.cloudbees.plugins.credentials.CredentialsScope
import com.cloudbees.plugins.credentials.domains.Domain
import com.cloudbees.plugins.credentials.impl.UsernamePasswordCredentialsImpl
import com.cloudbees.plugins.credentials.SecretBytes
import com.cloudbees.hudson.plugins.folder.Folder
import com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider.FolderCredentialsProperty
import org.jenkinsci.plugins.plaincredentials.impl.FileCredentialsImpl
import org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl
import org.jenkinsci.plugins.github_branch_source.GitHubAppCredentials
import hudson.util.Secret
import javaposse.jobdsl.plugin.JenkinsJobManagement
import javaposse.jobdsl.dsl.DslScriptLoader
import hudson.plugins.sonar.SonarGlobalConfiguration
import hudson.plugins.sonar.SonarInstallation
import hudson.plugins.sonar.SonarRunnerInstallation
import hudson.plugins.sonar.model.TriggersConfig
import java.nio.file.Files
import java.nio.file.attribute.PosixFilePermissions

def j = Jenkins.get()
def configuration = new JsonSlurper().parse(new File('/etc/battlefield-ci/bootstrap.json'))
def completed = new File(j.rootDir, 'battlefield-bootstrap-complete')
completed.delete()
def savePrivate = { String name, String value ->
    def path = new File(j.rootDir, name).toPath()
    if (Files.isSymbolicLink(path)) { throw new IllegalStateException('Redirected bootstrap output') }
    if (!Files.exists(path)) { Files.createFile(path, PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString('rw-------'))) }
    Files.setPosixFilePermissions(path, PosixFilePermissions.fromString('rw-------'))
    Files.writeString(path, value)
}
if (configuration.mode == 'install' && !new File(j.rootDir, 'battlefield-bootstrap-api-token').exists()) {
    if (!(j.securityRealm instanceof HudsonPrivateSecurityRealm)) { throw new IllegalStateException('Expected local security realm for first install') }
    def user = User.getById(configuration.admin, false)
    if (user == null) {
        user = j.securityRealm.createAccount(configuration.admin, configuration.adminPassword)
    } else if (!user.getProperty(HudsonPrivateSecurityRealm.Details)?.isPasswordCorrect(configuration.adminPassword)) {
        throw new IllegalStateException('Interrupted install administrator differs; refusing to replace its identity')
    }
    def token = user.getProperty(ApiTokenProperty).tokenStore.generateNewToken('battlefield-external-bootstrap')
    user.save()
    savePrivate('battlefield-bootstrap-api-token', token.plainValue)
}
def expected = [
    'github-app-battlefield': GitHubAppCredentials,
    'battlefield-ghcr': UsernamePasswordCredentialsImpl,
    'battlefield-cosign-key': FileCredentialsImpl,
    'battlefield-cosign-password': StringCredentialsImpl,
    'battlefield-cosign-public-key': FileCredentialsImpl,
    'battlefield-production-kubeconfig': FileCredentialsImpl,
    'battlefield-production-access-service-token': FileCredentialsImpl,
    'battlefield-production-application-env': FileCredentialsImpl,
    'sonarqube-battlefield-token': StringCredentialsImpl
]
def globalStore = SystemCredentialsProvider.getInstance().getStore()
def makeCredential = { String id, Map data ->
    switch (data.type) {
        case 'file': return new FileCredentialsImpl(CredentialsScope.GLOBAL, id, 'battlefield-platform-managed-v1', id, SecretBytes.fromBytes(data.value.getBytes('UTF-8')))
        case 'string': return new StringCredentialsImpl(CredentialsScope.GLOBAL, id, 'battlefield-ci-managed-v1', Secret.fromString(data.value))
        case 'usernamePassword': return new UsernamePasswordCredentialsImpl(CredentialsScope.GLOBAL, id, 'battlefield-ci-managed-v1', data.username, data.password)
        case 'githubApp': return new GitHubAppCredentials(CredentialsScope.GLOBAL, id, 'battlefield-ci-managed-v1', data.appId, Secret.fromString(data.privateKey))
        default: throw new IllegalStateException('Unknown CI credential type')
    }
}
expected.each { id, type ->
    def current = globalStore.getCredentials(Domain.global()).find { it.id == id }
    if (current == null) {
        def value = id == 'sonarqube-battlefield-token' ? (configuration.sonarToken != null ? [type: 'string', value: configuration.sonarToken] : null) : configuration.credentials?.get(id)
        if (value == null) { throw new IllegalStateException('Missing declared CI credential: ' + id) }
        globalStore.addCredentials(Domain.global(), makeCredential(id, value))
    } else if (!type.isInstance(current)) {
        throw new IllegalStateException('Existing CI credential has a different type: ' + id)
    } else if (id == 'sonarqube-battlefield-token' && configuration.sonarToken != null && current.secret.plainText != configuration.sonarToken) {
        throw new IllegalStateException('Imported Sonar token differs from existing Jenkins credential; no rotation performed')
    }
}
def folder = j.getItem('battlefield-platform')
if (folder == null) {
    folder = j.createProject(Folder, 'battlefield-platform')
    folder.description = 'battlefield-platform-managed-v1'
    folder.save()
}
if (!(folder instanceof Folder) || !folder.description?.startsWith('battlefield-platform-managed-v1')) {
    throw new IllegalStateException('Unowned platform folder')
}
def property = folder.properties.get(FolderCredentialsProperty)
if (property == null) {
    folder.addProperty(new FolderCredentialsProperty([]))
    property = folder.properties.get(FolderCredentialsProperty)
}
def folderStore = property.store
['host-inventory', 'host-inputs', 'host-vault-password', 'host-sudo-password'].each { id ->
    def current = folderStore.getCredentials(Domain.global()).find { it.id == id }
    if (current == null) {
        def value = configuration.credentials?.get(id)
        if (value == null) { throw new IllegalStateException('Missing platform credential: ' + id) }
        folderStore.addCredentials(Domain.global(), makeCredential(id, value))
    } else if (!(current instanceof FileCredentialsImpl) || current.description != 'battlefield-platform-managed-v1') {
        throw new IllegalStateException('Unowned or wrong-type platform credential: ' + id)
    }
}
def node = j.getNode('battlefield-local')
if (node == null) {
    def launcher = new JNLPLauncher()
    launcher.webSocket = true
    node = new DumbSlave('battlefield-local', configuration.home + '/jenkins-agent', launcher)
    node.numExecutors = 4
    node.labelString = 'local-kind battlefield-production'
    node.mode = Node.Mode.NORMAL
    j.addNode(node)
}
if (node.remoteFS != configuration.home + '/jenkins-agent' || node.numExecutors != 4 ||
        !node.labelString.tokenize().contains('battlefield-production') || !(node.launcher instanceof JNLPLauncher)) {
    throw new IllegalStateException('Existing agent differs from registered shared-host layout')
}
savePrivate('battlefield-agent-secret', node.toComputer().jnlpMac)
// Merge only our named integrations; preserve other Sonar servers and tool installations.
def sonar = j.getDescriptorByType(SonarGlobalConfiguration)
def integrations = sonar.installations.findAll { it.name != 'battlefield-sonarqube' }
integrations.add(new SonarInstallation('battlefield-sonarqube', 'http://127.0.0.1:9000', 'sonarqube-battlefield-token', null, null, '', '', '', null))
sonar.setInstallations(integrations as SonarInstallation[])
sonar.setBuildWrapperEnabled(true)
sonar.save()
def tools = j.getDescriptorByType(SonarRunnerInstallation.DescriptorImpl)
def scanners = tools.installations.findAll { it.name != 'SonarScanner' }
scanners.add(new SonarRunnerInstallation('SonarScanner', configuration.scannerHome, []))
tools.setInstallations(scanners as SonarRunnerInstallation[])
tools.save()
// Job DSL runs only for missing top-level jobs. Existing multibranch properties,
// folder credentials, history, trigger state and discovery traits are untouched.
def manager = new JenkinsJobManagement(System.out, [:], new File('/opt/battlefield-ci'))
['autonomous-battlefield': 'repository.groovy', 'battlefield-assurance': 'assurance.groovy'].each { name, source ->
    if (j.getItem(name) == null) {
        new DslScriptLoader(manager).runScript(new File('/opt/battlefield-ci/infra/jenkins/' + source).text)
    }
}
j.save()
savePrivate('battlefield-bootstrap-complete', 'battlefield-ci-managed-v1\n')
