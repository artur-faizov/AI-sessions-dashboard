package arturfaizov.aisessions;

import com.intellij.execution.configurations.GeneralCommandLine;
import com.intellij.execution.process.OSProcessHandler;
import com.intellij.execution.process.ProcessEvent;
import com.intellij.execution.process.ProcessListener;
import com.intellij.openapi.Disposable;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.application.PathManager;
import com.intellij.openapi.components.Service;
import com.intellij.openapi.util.Key;
import com.intellij.openapi.util.SystemInfo;
import com.intellij.util.system.CpuArch;
import org.jetbrains.annotations.NotNull;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.concurrent.CompletableFuture;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Runs the bundled dashboard server (one per IDE) and hands out its URL.
 *
 * <p>The binary lives in the plugin's {@code bin/} folder, next to {@code lib/}. It is started
 * without arguments, so it serves every provider merged into one view; {@code NO_BROWSER=1}
 * stops it from opening the system browser. The server picks its own port (8787, or the next
 * free one) and prints {@code http://localhost:<port>}, which is read from its output.
 */
@Service(Service.Level.APP)
public final class DashboardService implements Disposable {
    private static final Pattern URL = Pattern.compile("http://localhost:\\d+");

    private OSProcessHandler handler;
    private CompletableFuture<String> url;

    public static DashboardService getInstance() {
        return ApplicationManager.getApplication().getService(DashboardService.class);
    }

    /** Starts the server unless it is already running; completes with its URL. */
    public synchronized CompletableFuture<String> start() {
        if (handler != null && !handler.isProcessTerminated()) {
            return url;
        }
        CompletableFuture<String> future = new CompletableFuture<>();
        url = future;
        try {
            Path bin = binary();
            bin.toFile().setExecutable(true);
            GeneralCommandLine cmd = new GeneralCommandLine(bin.toString())
                    .withParentEnvironmentType(GeneralCommandLine.ParentEnvironmentType.CONSOLE)
                    .withEnvironment("NO_BROWSER", "1");
            StringBuilder log = new StringBuilder();
            handler = new OSProcessHandler(cmd);
            handler.addProcessListener(new ProcessListener() {
                @Override
                public void onTextAvailable(@NotNull ProcessEvent event, @NotNull Key outputType) {
                    synchronized (log) {
                        log.append(event.getText());
                    }
                    Matcher m = URL.matcher(event.getText());
                    if (m.find()) {
                        future.complete(m.group());
                    }
                }

                @Override
                public void processTerminated(@NotNull ProcessEvent event) {
                    synchronized (log) {
                        future.completeExceptionally(new IllegalStateException(
                                "dashboard exited with code " + event.getExitCode() + "\n" + log));
                    }
                }
            });
            handler.startNotify();
        } catch (Exception e) {
            future.completeExceptionally(e);
        }
        return future;
    }

    /** File name of the dashboard binary for this OS/CPU, as produced by {@code make build-all}. */
    static String binaryName() {
        if (SystemInfo.isWindows) {
            return "dashboard.exe";
        }
        boolean arm = CpuArch.isArm64();
        if (SystemInfo.isMac) {
            return arm ? "dashboard-mac-darwin" : "dashboard-mac-darwin-amd64";
        }
        return arm ? "dashboard-lin-arm64" : "dashboard-lin-x64";
    }

    /** {@code <plugin>/lib/<jar>} → {@code <plugin>/bin/<binary>}. */
    private static Path binary() {
        String jar = PathManager.getJarPathForClass(DashboardService.class);
        if (jar == null) {
            throw new IllegalStateException("cannot locate the plugin directory");
        }
        Path bin = Path.of(jar).getParent().getParent().resolve("bin").resolve(binaryName());
        if (!Files.isRegularFile(bin)) {
            throw new IllegalStateException("this plugin build has no dashboard for your OS/CPU (expected "
                    + bin.getFileName() + ") — install the plugin zip for your platform");
        }
        return bin;
    }

    @Override
    public synchronized void dispose() {
        if (handler != null) {
            handler.destroyProcess();
        }
    }
}
