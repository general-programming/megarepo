import React, { Component } from 'react';
import withStyles from '@material-ui/core/styles/withStyles';
import Typography from '@material-ui/core/Typography';
import Paper from '@material-ui/core/Paper';
import Avatar from '@material-ui/core/Avatar';
import DescriptionIcon from '@material-ui/icons/Description';
import Button from '@material-ui/core/Button';

const styles = theme => ({
    root: {
        marginBottom: theme.spacing.unit,
    },
    paper: {
        padding: theme.spacing.unit * 3,
        textAlign: 'left',
        color: theme.palette.text.secondary
    },
    avatar: {
        margin: 10,
        backgroundColor: theme.palette.grey['200'],
        color: theme.palette.text.primary,
    },
    avatarContainer: {
        [theme.breakpoints.down('sm')]: {
            marginLeft: 0,
            marginBottom: theme.spacing.unit * 4
        },
    },
    itemContainer: {
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        [theme.breakpoints.down('sm')]: {
            display: 'flex',
            flexDirection: 'column',
            justifyContent: 'center'
        },
    },
    baseline: {
        alignSelf: 'baseline',
        [theme.breakpoints.down('sm')]: {
            display: 'flex',
            flexDirection: 'column',
            textAlign: 'center',
            alignItems: 'center',
            width: '100%',
            marginTop: theme.spacing.unit * 2,
            marginBottom: theme.spacing.unit * 2,
            marginLeft: 0
        },
    },
    inline: {
        display: 'inline-block',
        marginLeft: theme.spacing.unit * 4,
        [theme.breakpoints.down('sm')]: {
            marginLeft: 0
        },
    },
    inlineRight: {
        width: '30%',
        textAlign: 'right',
        marginLeft: 50,
        alignSelf: 'flex-end',
        [theme.breakpoints.down('sm')]: {
            width: '100%',
            margin: 0,
            textAlign: 'center'
        },
    },
});

class ProductItem extends Component {
    render() {
        const { classes } = this.props;

        return (
            <div className={classes.root}>
                <Paper className={classes.paper}>
                    <div className={classes.itemContainer}>
                        {/* <div className={classes.avatarContainer}>
                            <Avatar className={classes.avatar}>
                                <DescriptionIcon />
                            </Avatar>
                        </div> */}
                        <div className={classes.baseline}>
                            <div className={classes.inline}>
                                <Typography style={{ textTransform: 'uppercase' }} color='secondary' gutterBottom>
                                    Name
                                </Typography>
                                <Typography variant="h6" gutterBottom>
                                    Yiff Delivery
                                </Typography>
                            </div>
                            <div className={classes.inline}>
                                <Typography style={{ textTransform: 'uppercase' }} color='secondary' gutterBottom>
                                    Reccurence
                                </Typography>
                                <Typography variant="h6" gutterBottom>
                                    Monthly
                                </Typography>
                            </div>
                            <div className={classes.inline}>
                                <Typography style={{ textTransform: 'uppercase' }} color='secondary' gutterBottom>
                                    Creation date
                                </Typography>
                                <Typography variant="h6" gutterBottom>
                                    01 February 2019
                                </Typography>
                            </div>
                            <div className={classes.inline}>
                                <Typography style={{ textTransform: 'uppercase' }} color='secondary' gutterBottom>
                                    Amount
                                </Typography>
                                <Typography variant="h6" gutterBottom>
                                    10 USD
                                </Typography>
                            </div>
                        </div>
                        <Button className={classes.button} variant="contained" color="primary">
                            Unsubscribe
                        </Button>
                    </div>
                </Paper>
            </div>
        )
    }
}

export default withStyles(styles)(ProductItem);
