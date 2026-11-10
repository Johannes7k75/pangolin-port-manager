#!/usr/bin/env python3
import os
import sys
from playwright.sync_api import sync_playwright, expect


def run_e2e(base_url: str, screenshot_dir: str = ""):
    chromium_bin = os.environ.get("CHROMIUM_BIN", "/usr/bin/chromium")
    with sync_playwright() as p:
        browser = p.chromium.launch(
            executable_path=chromium_bin,
            args=["--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage"],
        )
        context = browser.new_context(viewport={"width": 1200, "height": 800})
        page = context.new_page()

        # Automatically accept confirmation dialogs (e.g. Delete confirmation)
        page.on("dialog", lambda dialog: dialog.accept())

        # 1. Load Dashboard
        page.goto(base_url, wait_until="networkidle")
        expect(page.locator(".breadcrumb-title")).to_have_text("ports")
        expect(page.locator(".empty-state-title")).to_have_text("No port mappings configured")

        # 2. Open Add Port Modal & create 25565/tcp ("Minecraft")
        page.click("#btn-open-add")
        expect(page.locator("#add-modal")).not_to_have_class("modal-overlay hidden")

        page.fill("#add-name", "Minecraft")
        page.fill("#add-port", "25565")
        page.select_option("#add-protocol", "tcp")
        page.fill("#add-resource", "mc-survival")
        page.click("#add-submit-btn")

        # Modal must close automatically on success
        expect(page.locator("#add-modal")).to_have_class("modal-overlay hidden")

        # Verify table row and pending changes status banner appear immediately without reload
        expect(page.locator("#ports-count")).to_have_text("1")
        expect(page.locator("#ports-container")).to_contain_text("25565/tcp")
        expect(page.locator("#ports-container")).to_contain_text("Minecraft")
        expect(page.locator("#ports-container")).to_contain_text("mc-survival")
        expect(page.locator("#status-banner .status-title")).to_have_text("Unapplied changes")
        expect(page.locator("#apply-btn")).to_be_visible()

        if screenshot_dir:
            page.screenshot(path=os.path.join(screenshot_dir, "01_dashboard_pending.png"))

        # 3. Try creating a duplicate mapping (25565/tcp) -> Visual error MUST be displayed!
        page.click("#btn-open-add")
        expect(page.locator("#add-modal")).not_to_have_class("modal-overlay hidden")
        page.fill("#add-name", "Duplicate Server")
        page.fill("#add-port", "25565")
        page.select_option("#add-protocol", "tcp")
        page.click("#add-submit-btn")

        # Modal MUST stay open and #form-error must display the duplicate error box visibly
        error_box = page.locator("#form-error .form-error-box")
        expect(error_box).to_be_visible()
        expect(error_box).to_contain_text("a rule with this port and protocol already exists: 25565/tcp (Minecraft)")
        expect(page.locator("#add-modal")).not_to_have_class("modal-overlay hidden")

        if screenshot_dir:
            page.screenshot(path=os.path.join(screenshot_dir, "02_duplicate_port_error.png"))

        # Close add modal
        page.click("#add-modal .modal-close")
        expect(page.locator("#add-modal")).to_have_class("modal-overlay hidden")

        # 4. Edit the existing mapping (change Minecraft 25565 -> Minecraft Java 25566)
        page.click(".edit-rule-btn")
        expect(page.locator("#edit-modal")).not_to_have_class("modal-overlay hidden")
        expect(page.locator("#edit-name")).to_have_value("Minecraft")
        expect(page.locator("#edit-port")).to_have_value("25565")

        if screenshot_dir:
            page.screenshot(path=os.path.join(screenshot_dir, "03_edit_modal.png"))

        page.fill("#edit-name", "Minecraft Java")
        page.fill("#edit-port", "25566")
        page.fill("#edit-resource", "mc-java-prod")
        page.click("#edit-submit-btn")

        # Edit modal closes on success and table updates to 25566/tcp
        expect(page.locator("#edit-modal")).to_have_class("modal-overlay hidden")
        expect(page.locator("#ports-container")).to_contain_text("25566/tcp")
        expect(page.locator("#ports-container")).to_contain_text("tcp-25566")
        expect(page.locator("#ports-container")).to_contain_text("Minecraft Java")
        expect(page.locator("#ports-container")).to_contain_text("mc-java-prod")

        # 5. Click Apply changes -> verifies docker compose config succeeds without conflict
        page.click("#apply-btn")
        expect(page.locator("#status-banner .status-title")).to_have_text("Synced")

        if screenshot_dir:
            page.screenshot(path=os.path.join(screenshot_dir, "04_synced_state.png"))

        # 6. Toggle Disable -> Verify immediate transition to "Unapplied changes" without reload!
        page.click(".toggle-rule-btn")
        expect(page.locator("#ports-container .badge")).to_have_text("Disabled")
        expect(page.locator("#status-banner .status-title")).to_have_text("Unapplied changes")
        expect(page.locator("#apply-btn")).to_be_visible()

        # Apply the disabled state
        page.click("#apply-btn")
        expect(page.locator("#status-banner .status-title")).to_have_text("Synced")

        # Re-enable -> Verify immediate transition to "Unapplied changes" again
        page.click(".toggle-rule-btn")
        expect(page.locator("#ports-container .badge")).to_have_text("Active")
        expect(page.locator("#status-banner .status-title")).to_have_text("Unapplied changes")
        expect(page.locator("#apply-btn")).to_be_visible()

        # Apply again
        page.click("#apply-btn")
        expect(page.locator("#status-banner .status-title")).to_have_text("Synced")

        # 7. Delete rule -> Verify empty state and Unapplied changes banner
        page.click(".delete-rule-btn")
        expect(page.locator(".empty-state-title")).to_have_text("No port mappings configured")
        expect(page.locator("#ports-count")).to_have_text("0")
        expect(page.locator("#status-banner .status-title")).to_have_text("Unapplied changes")

        browser.close()


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: playwright_e2e.py <base_url> [screenshot_dir]", file=sys.stderr)
        sys.exit(1)
    url = sys.argv[1]
    shots = sys.argv[2] if len(sys.argv) > 2 else os.environ.get("SCREENSHOT_DIR", "")
    run_e2e(url, shots)
