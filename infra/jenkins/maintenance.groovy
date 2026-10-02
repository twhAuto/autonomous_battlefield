// Keep queued jobs paused across an external maintenance restart.
import jenkins.model.Jenkins
if (new File(Jenkins.get().rootDir, 'battlefield-maintenance').exists()) {
    Jenkins.get().doQuietDown()
}
