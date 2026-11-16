package arturfaizov.aisessions;

import com.intellij.icons.AllIcons;
import com.intellij.ide.BrowserUtil;
import com.intellij.openapi.Disposable;
import com.intellij.openapi.actionSystem.AnAction;
import com.intellij.openapi.actionSystem.AnActionEvent;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.application.ModalityState;
import com.intellij.openapi.project.DumbAware;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.wm.ToolWindow;
import com.intellij.openapi.wm.ToolWindowFactory;
import com.intellij.ui.components.JBScrollPane;
import com.intellij.ui.content.Content;
import com.intellij.ui.content.ContentFactory;
import org.jetbrains.annotations.NotNull;

import javax.swing.BorderFactory;
import javax.swing.JComponent;
import javax.swing.JPanel;
import javax.swing.JTextArea;
import java.awt.BorderLayout;
import java.util.List;
import java.util.concurrent.CompletionException;

/** The "AI Sessions" tool window: the dashboard in JCEF, with Reload and Open in Browser actions. */
public final class DashboardToolWindowFactory implements ToolWindowFactory, DumbAware {
    @Override
    public void createToolWindowContent(@NotNull Project project, @NotNull ToolWindow toolWindow) {
        DashboardPanel panel = new DashboardPanel();
        Content content = ContentFactory.getInstance().createContent(panel, "", false);
        content.setDisposer(panel);
        toolWindow.getContentManager().addContent(content);
        toolWindow.setTitleActions(List.of(
                new AnAction("Reload", "Reload the dashboard", AllIcons.Actions.Refresh) {
                    @Override
                    public void actionPerformed(@NotNull AnActionEvent e) {
                        panel.load();
                    }
                },
                new AnAction("Open in Browser", "Open the dashboard in the system browser",
                        AllIcons.Ide.External_link_arrow) {
                    @Override
                    public void actionPerformed(@NotNull AnActionEvent e) {
                        DashboardService.getInstance().start().thenAccept(BrowserUtil::browse);
                    }
                }));
        panel.load();
    }

    private static final class DashboardPanel extends JPanel implements Disposable {
        private BrowserView browser;
        private boolean disposed;

        DashboardPanel() {
            super(new BorderLayout());
        }

        /** Starts the server if needed and shows (or reloads) the dashboard. */
        void load() {
            if (browser == null) {
                show(message("Starting dashboard…"));
            }
            DashboardService.getInstance().start().whenComplete((url, err) ->
                    ApplicationManager.getApplication().invokeLater(() -> {
                        if (err != null) {
                            Throwable cause = err instanceof CompletionException && err.getCause() != null
                                    ? err.getCause() : err;
                            show(message("Dashboard failed to start: " + cause.getMessage()));
                            return;
                        }
                        if (!jcefSupported()) {
                            show(message("This IDE has no embedded browser (JCEF).\n"
                                    + "Use the \"Open in Browser\" button above: " + url));
                            return;
                        }
                        if (browser == null) {
                            browser = new BrowserView(this, url);
                        } else {
                            browser.load(url);
                        }
                        show(browser.component());
                    }, ModalityState.any(), o -> disposed));
        }

        /** False when JCEF is missing: its classes then fail to link. */
        private static boolean jcefSupported() {
            try {
                return BrowserView.isSupported();
            } catch (LinkageError e) {
                return false;
            }
        }

        private void show(JComponent c) {
            removeAll();
            add(c, BorderLayout.CENTER);
            revalidate();
            repaint();
        }

        private static JComponent message(String text) {
            JTextArea area = new JTextArea(text);
            area.setEditable(false);
            area.setLineWrap(true);
            area.setWrapStyleWord(true);
            area.setBorder(BorderFactory.createEmptyBorder(8, 8, 8, 8));
            return new JBScrollPane(area);
        }

        @Override
        public void dispose() {
            disposed = true;
        }
    }
}
