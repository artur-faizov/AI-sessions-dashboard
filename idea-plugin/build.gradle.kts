import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType

plugins {
    java
    id("org.jetbrains.intellij.platform") version "2.20.0"
}

group = "arturfaizov.aisessions"
version = providers.gradleProperty("pluginVersion").get()

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        create(
            IntelliJPlatformType.fromCode(providers.gradleProperty("platformType").get()),
            providers.gradleProperty("platformVersion").get(),
        )
        // JCEF (JBCefBrowser) is its own bundled plugin since 2026.2; plugin.xml depends on it optionally.
        bundledPlugin("com.intellij.modules.jcef")
    }
}

// Java 17 bytecode: the JetBrains Runtime of 2023.3 (build 233) is Java 17.
java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}
tasks.withType<JavaCompile>().configureEach {
    options.release = 17
    options.encoding = "UTF-8"
}

intellijPlatform {
    buildSearchableOptions = false
    // No @NotNull runtime assertions in the bytecode (as in 3.1.1).
    instrumentCode = false
    pluginConfiguration {
        version = providers.gradleProperty("pluginVersion")
        ideaVersion {
            sinceBuild = providers.gradleProperty("pluginSinceBuild")
            untilBuild = provider { null }
        }
    }
    pluginVerification {
        // The IDE the plugin is built against (already downloaded); add recommended()
        // to check every recommended IDE version (several large downloads).
        ides {
            create(
                IntelliJPlatformType.fromCode(providers.gradleProperty("platformType").get()),
                providers.gradleProperty("platformVersion").get(),
            )
        }
    }
}

// The dashboard binaries built by `make build-all`; the plugin starts the one for the
// current OS/CPU from <plugin>/bin/ (see DashboardService.binary()).
val dashboardBinaries = listOf("dashboard-mac-darwin", "dashboard-lin-x64", "dashboard-lin-arm64", "dashboard.exe")
val dashboardDist = layout.projectDirectory.dir(providers.gradleProperty("dashboardDist").get())

val checkDashboardBinaries = tasks.register("checkDashboardBinaries") {
    val dist = dashboardDist.asFile
    val names = dashboardBinaries
    doLast {
        val missing = names.filterNot { dist.resolve(it).isFile }
        if (missing.isNotEmpty()) {
            throw GradleException("missing dashboard binaries in $dist: $missing — run `make build-all` first")
        }
    }
}

tasks.prepareSandbox {
    dependsOn(checkDashboardBinaries)
    from(dashboardDist) {
        include(dashboardBinaries)
        into(intellijPlatform.projectName.map { "$it/bin" })
    }
}

// Gradle 9 archives use fixed 644 file modes; the Linux and macOS binaries must stay executable.
tasks.buildPlugin {
    filesMatching("**/bin/dashboard-*") {
        permissions { unix("rwxr-xr-x") }
    }
}
