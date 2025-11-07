#!/usr/bin/env python3
"""
Helper per sincronizzare file con GitHub dal bot
"""
import os
import subprocess
import sys
from pathlib import Path

def run_command(cmd, cwd=None):
    """Esegue comando e ritorna output"""
    result = subprocess.run(cmd, shell=True, capture_output=True, text=True, cwd=cwd)
    if result.returncode != 0:
        print(f"❌ Errore comando: {cmd}")
        print(f"   Output: {result.stderr}")
        return False
    return True

def setup_git():
    """Configura git"""
    run_command('git config --global user.email "render-bot@octotracker.app"')
    run_command('git config --global user.name "Render Bot"')

def git_pull():
    """Fa pull delle modifiche da GitHub"""
    token = os.getenv('GITHUB_TOKEN')
    repo = os.getenv('GITHUB_REPO', 'dstmrk/octotracker')
    branch = os.getenv('GITHUB_BRANCH', 'main')

    if not token:
        print("⚠️  GITHUB_TOKEN non impostato, skip git pull")
        return False

    print("🔄 Git pull...")
    setup_git()

    # Verifica se siamo in un repo git
    if not Path('.git').exists():
        print("❌ Non siamo in un repository git")
        return False

    # Pull con authentication
    success = run_command(f'git pull https://{token}@github.com/{repo}.git {branch}')
    if success:
        print("✅ Git pull completato")
    return success

def git_push(file_path, commit_message):
    """Fa commit e push di un file specifico"""
    token = os.getenv('GITHUB_TOKEN')
    repo = os.getenv('GITHUB_REPO', 'dstmrk/octotracker')
    branch = os.getenv('GITHUB_BRANCH', 'main')

    if not token:
        print("⚠️  GITHUB_TOKEN non impostato, skip git push")
        return False

    print(f"📤 Git push di {file_path}...")
    setup_git()

    # Add file
    if not run_command(f'git add {file_path}'):
        return False

    # Check if there are changes
    result = subprocess.run('git diff --staged --quiet', shell=True)
    if result.returncode == 0:
        print("ℹ️  Nessuna modifica da committare")
        return True

    # Commit
    safe_message = commit_message.replace('"', '\\"')
    if not run_command(f'git commit -m "{safe_message}"'):
        return False

    # Push
    success = run_command(f'git push https://{token}@github.com/{repo}.git HEAD:{branch}')
    if success:
        print("✅ Git push completato")
    return success

if __name__ == '__main__':
    # Test
    if len(sys.argv) > 1 and sys.argv[1] == 'pull':
        git_pull()
    elif len(sys.argv) > 2 and sys.argv[1] == 'push':
        git_push(sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else 'Update from bot')
