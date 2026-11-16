package arturfaizov.aisessions;

import com.intellij.openapi.Disposable;
import com.intellij.openapi.util.Disposer;
import com.intellij.ui.jcef.JBCefApp;
import com.intellij.ui.jcef.JBCefBrowser;

import javax.swing.JComponent;

/**
 * The embedded JCEF browser. Kept in its own class so that the JCEF classes are
 * only loaded when JCEF is present (it is an optional dependency since 2026.2).
 */
final class BrowserView {
    private final JBCefBrowser browser;

    static boolean isSupported() {
        return JBCefApp.isSupported();
    }

    BrowserView(Disposable parent, String url) {
        browser = new JBCefBrowser(url);
        Disposer.register(parent, browser);
    }

    JComponent component() {
        return browser.getComponent();
    }

    void load(String url) {
        browser.loadURL(url);
    }
}
