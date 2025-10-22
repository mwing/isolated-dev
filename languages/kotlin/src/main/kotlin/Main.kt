/**
 * {{PROJECT_NAME}} - A Kotlin application
 */

fun main() {
    println("Hello from {{PROJECT_NAME}}!")
    println("Kotlin development environment is ready!")
    
    // Example HTTP server (uncomment to use with Ktor)
    /*
    embeddedServer(Netty, port = 8080, host = "0.0.0.0") {
        routing {
            get("/") {
                call.respondText("Hello from {{PROJECT_NAME}}!")
            }
        }
    }.start(wait = true)
    */
}