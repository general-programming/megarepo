import React from 'react';
import withStyles from '@material-ui/core/styles/withStyles';

import AppHeader from '../Header/AppHeader';

import ProfileInfo from './ProfileInfo';
import ProfileProducts from './ProfileProducts';

const styles = theme => ({
    layout: {
        width: 'auto',
        marginLeft: theme.spacing.unit * 3,
        marginRight: theme.spacing.unit * 3,
        marginTop: theme.spacing.unit * 2,
        [theme.breakpoints.up(1100 + theme.spacing.unit * 3 * 2)]: {
          width: 1100,
          marginLeft: 'auto',
          marginRight: 'auto',
        },
    },
});

function ProfilePage(props) {
    const { classes } = props;

    return (
        <React.Fragment>
            <AppHeader />
            <div className={classes.layout}>
                <ProfileInfo />
                <ProfileProducts />
            </div>
        </React.Fragment>
    );
}

export default withStyles(styles)(ProfilePage);
