// TODO: GET THIS DYNAMICALLY
const selfProject = "19403";

let styles = document.createElement("style");
styles.innerHTML = `
.followbutton {
    background-color: #83254f;
    color: white;
    border-radius: 2px;
    padding: 6px 9px;
    line-height: 1;
}`;
document.head.appendChild(styles);

let getFollowers = async () => {
    let res = {};
    let followers = await fetch(
        "https://cohost.org/api/v1/projects/following?limit=9999"
    )
        .then((res) => res.json())
        .then((res) => res.projects);
    followers.forEach((follower) => {
        res[follower.handle] = follower.projectId;
    });

    return res;
};

let followUser = async (username) => {
    console.log("following", username);

    // get the project id from the user page
    let projectId = await fetch(`https://cohost.org/${username}`)
        .then((res) => res.text())
        .then((res) => {
            let match = res.match(/"projectId":(\d+)/);
            if (match) {
                return match[1];
            }
        });

    // send the follow request
    await fetch(
        `https://cohost.org/rc/relationships/project-${selfProject}/to-project-${projectId}/create-follow-request`,
        {
            method: "POST",
        }
    )
        .then((res) => res.json())
        .then((res) => {
            console.log(res);
        });

    // yeet the follow button elements
    let followButtons = document.querySelectorAll(
        `.followbutton[data-username="${username}"]`
    );
    followButtons.forEach((followButton) => {
        followButton.remove();
    });
};

let followers = await getFollowers();

document.querySelectorAll("a[rel='author']").forEach((elem) => {
    let username = new URL(elem.href).pathname.split("/")[1];
    console.log(username);
    if (!username) return;

    let buttonElement = document.createElement("button");
    buttonElement.classList.add("followbutton");
    buttonElement.textContent = "follow";
    buttonElement.dataset.username = username;

    buttonElement.onclick = async () => {
        await followUser(username);
        return false;
    };

    if (!followers[username]) {
        elem.insertAdjacentElement("afterend", buttonElement);
    }
});
